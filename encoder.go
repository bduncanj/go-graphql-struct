package gqlstruct

import (
	"fmt"
	"reflect"
	"unicode"

	"github.com/graphql-go/graphql"
)

type encoder struct {
	types map[string]graphql.Type
}

func NewEncoder() *encoder {
	return &encoder{
		types: make(map[string]graphql.Type),
	}
}

var defaultEncoder = NewEncoder()

func (enc *encoder) Struct(obj interface{}, options ...Option) (*graphql.Object, error) {
	t := reflect.TypeOf(obj)
	return enc.StructOf(t, options...)
}

func (enc *encoder) Args(obj interface{}) (graphql.FieldConfigArgument, error) {
	t := reflect.TypeOf(obj)
	return enc.ArgsOf(t)
}

func getIsUpperCase(fieldName []rune, i int) bool {
	upperCase := false

	if i == 0 {
		return false
	}

	lastLetter := (len(fieldName) == i+1)
	nextToLastLetter := (len(fieldName) == (i + 2))

	// Transition A: Previous lower, this upper
	wordBoundaryA := unicode.IsLower(fieldName[i-1]) && unicode.IsUpper(fieldName[i])
	if lastLetter && wordBoundaryA {
		return true
	}

	// Handle single 's' pluralization after this letter
	if nextToLastLetter && (fieldName[i+1] == 's') {
		return false
	}

	if !lastLetter {

		// Transition B: This letter upper, next is lower
		wordBoundaryB := unicode.IsUpper(fieldName[i]) && unicode.IsLower(fieldName[i+1])

		if wordBoundaryB || wordBoundaryA {
			return true
		}
	}
	return upperCase
}

func toLowerCamelCase(input string) string {

	inFieldName := []rune(input)
	outFieldName := make([]rune, len(inFieldName))

	for i := 0; i < len(inFieldName); i++ {
		// Be default make everything lower case
		upperCase := getIsUpperCase(inFieldName, i)

		if upperCase {
			outFieldName[i] = unicode.ToUpper(inFieldName[i])
		} else {
			outFieldName[i] = unicode.ToLower(inFieldName[i])
		}

	}
	return string(outFieldName)
}

// Struct returns a `*graphql.Object` with the description extracted from the
// obj passed.
//
// This method extracts the information needed from the fields of the obj
// informed. All fields tagged with "graphql" are added.
//
// The "graphql" tag can be defined as:
//
// ```
// type T struct {
//     field string `graphql:"fieldname"`
// }
// ```
//
// * fieldname: The name of the field.
func (enc *encoder) StructOf(t reflect.Type, options ...Option) (*graphql.Object, error) {
	if r, ok := enc.getType(t, false); ok {
		if d, ok := r.(*graphql.Object); ok {
			return d, nil
		}
		return nil, fmt.Errorf("%s is not an graphql.Object", r)
	}

	name := t.Name()
	if t.Kind() == reflect.Ptr {
		name = t.Elem().Name()
	}

	objCfg := graphql.ObjectConfig{
		Name:   name,
		Fields: graphql.Fields{},
	}

	// Apply options
	for _, opt := range options {
		err := opt.Apply(&objCfg)
		if err != nil {
			return nil, err
		}
	}

	r := graphql.NewObject(objCfg)
	enc.registerType(t, r, false)

	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	// Goes field by field of the object.

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag, ok := field.Tag.Lookup("graphql")
		// if !ok {
		// 	// If the field is not tagged, ignore it.
		// 	continue
		// }

		objectType, ok := enc.getType(field.Type, false)
		if !ok {
			ot, err := enc.buildFieldType(field.Type, false)
			if err != nil {
				return nil, NewErrTypeNotRecognizedWithStruct(err, t, field)
			}
			objectType = ot
			enc.registerType(field.Type, ot, false)
		}

		// If the tag starts with "!" it is a NonNull type.
		if len(tag) > 0 && tag[0] == '!' {
			objectType = graphql.NewNonNull(objectType)
			tag = tag[1:]
		}

		resolve := fieldResolve(field)

		gfield := &graphql.Field{
			Type:    objectType,
			Resolve: resolve,
		}
		desc, ok := field.Tag.Lookup("desc")
		if ok {
			gfield.Description = desc
		}
		dep, ok := field.Tag.Lookup("dep")
		if ok {
			gfield.DeprecationReason = dep
		}

		fieldName := []rune(field.Name)
		if len(tag) > 0 {
			fieldName = []rune(tag)
		}
		fieldNameS := toLowerCamelCase(string(fieldName))
		r.AddFieldConfig(fieldNameS, gfield)
	}
	return r, nil
}

func (enc *encoder) InputStructOf(t reflect.Type, options ...Option) (*graphql.InputObject, error) {
	if r, ok := enc.getType(t, true); ok {
		if d, ok := r.(*graphql.InputObject); ok {
			return d, nil
		}
		return nil, fmt.Errorf("%s is not an graphql.Object", r)
	}

	name := t.Name()
	if t.Kind() == reflect.Ptr {
		name = t.Elem().Name()
	}

	f, err := enc.InputObjectFieldMap(t)
	if err != nil {
		return nil, err
	}

	objCfg := graphql.InputObjectConfig{
		Name:   name + "Input",
		Fields: f,
	}

	// Apply options
	for _, opt := range options {
		err := opt.Apply(&objCfg)
		if err != nil {
			return nil, err
		}
	}

	r := graphql.NewInputObject(objCfg)
	enc.registerType(t, r, true)
	return r, nil
}

func (enc *encoder) FieldOf(t reflect.Type, options ...Option) (graphql.Field, error) {
	r := graphql.Field{}

	fieldType, err := enc.StructOf(t)
	if err != nil {
		return graphql.Field{}, err
	}
	r.Type = fieldType

	for _, option := range options {
		err = option.Apply(&r)
		if err != nil {
			return graphql.Field{}, err
		}
	}

	return r, nil
}

func (enc *encoder) Field(t interface{}, options ...Option) (graphql.Field, error) {
	return enc.FieldOf(reflect.TypeOf(t), options...)
}

func (enc *encoder) ArrayOf(t reflect.Type, options ...Option) (graphql.Type, error) {
	if t.Kind() == reflect.Ptr {
		// If pointer, get the Type of the pointer
		t = t.Elem()
	}
	var typeBuilt graphql.Type
	if cachedType, ok := enc.getType(t, false); ok {
		return graphql.NewList(cachedType), nil
	}
	if t.Kind() == reflect.Struct {
		bt, err := enc.StructOf(t, options...)
		if err != nil {
			return nil, err
		}
		typeBuilt = bt
	} else {
		ttt, err := enc.buildFieldType(t, false)
		if err != nil {
			return nil, err
		}
		typeBuilt = ttt
	}
	enc.registerType(t, typeBuilt, false)
	return graphql.NewList(typeBuilt), nil
}

func (enc *encoder) InputObjectFieldMap(t reflect.Type) (graphql.InputObjectConfigFieldMap, error) {
	r := graphql.InputObjectConfigFieldMap{}

	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Struct {
		return r, fmt.Errorf("cannot build args from a non struct")
	}

	// Goes field by field of the object.
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag, ok := field.Tag.Lookup("graphql")

		objectType, ok := enc.getType(field.Type, true)
		if !ok {
			ot, err := enc.buildFieldType(field.Type, true)
			if err != nil {
				return nil, NewErrTypeNotRecognizedWithStruct(err, t, field)
			}
			objectType = ot
			enc.registerType(field.Type, ot, true)
		}

		// If the tag starts with "!" it is a NonNull type.
		if len(tag) > 0 && tag[0] == '!' {
			objectType = graphql.NewNonNull(objectType)
			tag = tag[1:]
		}

		inputField := &graphql.InputObjectFieldConfig{
			Type: objectType,
		}

		fieldName := []rune(field.Name)
		if len(tag) > 0 {
			fieldName = []rune(tag)
		}
		fieldNameS := toLowerCamelCase(string(fieldName))

		r[fieldNameS] = inputField
	}

	return r, nil
}

func (enc *encoder) ArgsOf(t reflect.Type) (graphql.FieldConfigArgument, error) {
	r := graphql.FieldConfigArgument{}

	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Struct {
		return r, fmt.Errorf("cannot build args from a non struct")
	}

	// Goes field by field of the object.
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag, ok := field.Tag.Lookup("graphql")
		// if !ok {
		// 	// If the field is not tagged, ignore it.
		// 	continue
		// }

		objectType, ok := enc.getType(field.Type, true)
		if !ok {
			ot, err := enc.buildFieldType(field.Type, true)
			if err != nil {
				return nil, NewErrTypeNotRecognizedWithStruct(err, t, field)
			}
			objectType = ot
			enc.registerType(field.Type, ot, true)
		}

		// If the tag starts with "!" it is a NonNull type.
		if len(tag) > 0 && tag[0] == '!' {
			objectType = graphql.NewNonNull(objectType)
			tag = tag[1:]
		}

		graphQLArgument := &graphql.ArgumentConfig{
			Type: objectType,
		}

		fieldName := []rune(field.Name)
		if len(tag) > 0 {
			fieldName = []rune(tag)
		}
		fieldNameS := toLowerCamelCase(string(fieldName))

		r[fieldNameS] = graphQLArgument
	}

	return r, nil
}

func (enc *encoder) getType(t reflect.Type, isInput bool) (graphql.Type, bool) {
	name := t.Name()
	isStruct := t.Kind() == reflect.Struct
	if t.Kind() == reflect.Ptr {
		name = t.Elem().Name()
		isStruct = t.Elem().Kind() == reflect.Struct
	}
	if len(name) > 0 {
		if isStruct && isInput {
			gt, ok := enc.types[name+"Input"]
			return gt, ok
		}
		gt, ok := enc.types[name]
		return gt, ok
	}
	return nil, false
}

func (enc *encoder) registerType(t reflect.Type, r graphql.Type, isInput bool) {
	name := t.Name()
	isStruct := t.Kind() == reflect.Struct
	if t.Kind() == reflect.Ptr {
		name = t.Elem().Name()
		isStruct = t.Elem().Kind() == reflect.Struct
	}
	if len(name) > 0 {
		if isStruct && isInput {
			enc.types[name+"Input"] = r
		} else {
			enc.types[name] = r
		}
	}
}

func Struct(obj interface{}) *graphql.Object {
	r, err := defaultEncoder.Struct(obj)
	if err != nil {
		panic(err.Error())
	}
	return r
}

func InputObject(name string, obj interface{}) *graphql.InputObject {
	t := reflect.TypeOf(obj)
	r, err := defaultEncoder.InputObjectFieldMap(t)
	if err != nil {
		panic(err.Error())
	}
	return graphql.NewInputObject(
		graphql.InputObjectConfig{
			Name:   name,
			Fields: r,
		},
	)
}

// Args Obtain the arguments property of a mutation object
func Args(obj interface{}) graphql.FieldConfigArgument {
	r, err := defaultEncoder.Args(obj)
	if err != nil {
		panic(err.Error())
	}
	return r
}

func ArgsOf(t reflect.Type) graphql.FieldConfigArgument {
	r, err := defaultEncoder.ArgsOf(t)
	if err != nil {
		panic(err.Error())
	}
	return r
}

func FieldOf(t reflect.Type, options ...Option) (graphql.Field, error) {
	return defaultEncoder.FieldOf(t, options...)
}

func Field(t interface{}, options ...Option) graphql.Field {
	r, err := defaultEncoder.Field(t, options...)
	if err != nil {
		panic(err.Error())
	}
	return r
}
