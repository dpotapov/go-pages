package chtml

import (
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/fatih/camelcase"
)

// Scope defines an interface for managing arguments in a CHTML component. Scopes are organized
// in a hierarchical structure, with each scope potentially having a parent scope and multiple
// child scopes. Changes in a child scope propagate to its parent scope.
//
// A scope is closed when its associated component will not be rendered further. This occurs
// either when the HTTP request completes or when the component is removed from the page
// (e.g., due to c:if or c:for directives). Closing a parent scope results in the closure of
// all its child scopes.
//
// The CHTML component creates new scopes for each loop iteration, conditional branch, and
// component import using the Spawn method.
//
// This interface allows for custom implementations of components, enabling the inclusion of
// additional data such as HTTP request or WebSocket connection information.
type Scope interface {
	// Spawn creates a new child scope. It is initialized with variables that can be accessed from
	// the Component's Render() method using the Scope.Vars() method.
	Spawn(vars map[string]any) Scope

	// Vars provides access to variables stored in the scope.
	Vars() map[string]any

	// Touch marks the component as changed. The implementation should re-render the page
	// when this method is called.
	Touch()

	// DryRun indicates whether this is a dry run for validation purposes.
	// When in dry run mode, components should not fully render or modify state.
	DryRun() bool
}

// BaseScope is a base implementation of the Scope interface. For extra functionality, this type
// can be wrapped (embedded) in a custom scope implementation.
type BaseScope struct {
	vars    map[string]any
	touched chan struct{}
}

var _ Scope = (*BaseScope)(nil)

func NewBaseScope(vars map[string]any) *BaseScope {
	t := make(chan struct{}, 1)
	return &BaseScope{
		vars:    vars,
		touched: t,
	}
}

// Spawn creates a new child scope. If the current scope is closed, the new scope is also closed.
func (s *BaseScope) Spawn(vars map[string]any) Scope {
	return &BaseScope{
		vars:    vars,
		touched: s.touched, // all children share the same channel to notify root scope
	}
}

func (s *BaseScope) Vars() map[string]any {
	return s.vars
}

func (s *BaseScope) Touch() {
	select {
	case s.touched <- struct{}{}:
	default:
	}
}

// DryRun always returns false for regular scopes, indicating full rendering should occur.
func (s *BaseScope) DryRun() bool {
	return false
}

func (s *BaseScope) Touched() <-chan struct{} {
	return s.touched
}

// DryRunScope is a wrapper around a BaseScope that indicates a dry run.
// In dry run mode, components should not perform full rendering or modify state.
type DryRunScope struct {
	*BaseScope
}

// NewDryRunScope creates a new scope for validation purposes.
func NewDryRunScope(vars map[string]any) *DryRunScope {
	return &DryRunScope{
		BaseScope: NewBaseScope(vars),
	}
}

// Spawn creates a new child scope that preserves the dry run state.
func (s *DryRunScope) Spawn(vars map[string]any) Scope {
	return &DryRunScope{
		BaseScope: s.BaseScope.Spawn(vars).(*BaseScope),
	}
}

// DryRun returns true, indicating this is a dry run for validation.
func (s *DryRunScope) DryRun() bool {
	return true
}

// UnmarshalScope reads the variables from the scope and converts them to a provided target.
// The target must be a pointer to a struct or a map. The function returns an error if
// the target is not a pointer or if the scope variables cannot be converted to the target.
func UnmarshalScope(s Scope, target any) error {
	targetValue := reflect.ValueOf(target)
	if targetValue.Kind() != reflect.Ptr || targetValue.IsNil() {
		return errors.New("target must be a non-nil pointer")
	}
	targetElem := targetValue.Elem()

	vars := s.Vars()
	snakeCaseVars := make(map[string]any)
	for k, v := range vars {
		snakeCaseVars[toSnakeCase(k)] = v
	}

	switch targetElem.Kind() {
	case reflect.Struct:
		for i := 0; i < targetElem.NumField(); i++ {
			field := targetElem.Type().Field(i)
			fieldName := toSnakeCase(field.Name)
			val, ok := snakeCaseVars[fieldName]
			if !ok {
				if i == targetElem.NumField()-1 {
					val = snakeCaseVars["_"]
				} else {
					continue
				}
			}
			if val == nil {
				continue
			}
			fieldValue := targetElem.Field(i)
			rval := reflect.ValueOf(val)

			// Check if val is zero and if fieldValue can accept nil
			if !rval.IsValid() || (rval.Kind() == reflect.Ptr && rval.IsNil()) {
				// Check if fieldValue can accept nil
				if !fieldValue.CanSet() || (fieldValue.Kind() != reflect.Ptr && !fieldValue.CanAddr()) {
					return fmt.Errorf("cannot set nil value to field %s", field.Name)
				}
				rval = reflect.Zero(fieldValue.Type())
			}

			if d, err := decodeHook(rval, fieldValue); err != nil {
				return &DecodeError{
					Key: field.Name,
					Err: err,
				}
			} else {
				rval = reflect.ValueOf(d)
			}

			if rval.Type().ConvertibleTo(fieldValue.Type()) {
				fieldValue.Set(rval.Convert(fieldValue.Type()))
			} else {
				return fmt.Errorf("cannot convert value of field %s", field.Name)
			}
		}
	case reflect.Map:
		if targetElem.Type().Key().Kind() != reflect.String {
			return errors.New("map key must be a string")
		}

		if targetElem.IsNil() {
			targetElem.Set(reflect.MakeMap(targetElem.Type()))
		}
		for _, key := range targetElem.MapKeys() {
			k := toSnakeCase(key.String())
			if val, ok := snakeCaseVars[k]; ok {
				if val == nil {
					continue
				}
				val := reflect.ValueOf(val)

				// Check if val is zero and if map element type can accept nil
				if !val.IsValid() || (val.Kind() == reflect.Ptr && val.IsNil()) {
					val = reflect.Zero(targetElem.Type().Elem())
				}

				mapValue := targetElem.MapIndex(key)
				if mapValue.Kind() == reflect.Interface && !mapValue.IsNil() {
					mapValue = mapValue.Elem()
				}
				decodedVal, err := decodeHook(val, mapValue)
				if err != nil {
					return &DecodeError{
						Key: k,
						Err: err,
					}
				}

				targetElem.SetMapIndex(key, reflect.ValueOf(decodedVal))

				/*if val.Type().ConvertibleTo(targetElem.Type().Elem()) {
					targetElem.SetMapIndex(key, val.Convert(targetElem.Type().Elem()))
				} else {
					return fmt.Errorf("cannot convert value for map entry %s", k)
				}*/
			}
		}
	default:
		return errors.New("target must be a pointer to a struct or a map")
	}

	return nil
}

// MarshalScope stores the variables from the source in the scope. The source must be a struct
// or a map. The function returns an error if the source is not a struct or a map or if the
// source variables cannot be stored in the scope.
func MarshalScope(s Scope, src any) error {
	vars := s.Vars()
	srcValue := reflect.ValueOf(src)
	switch srcValue.Kind() {
	case reflect.Struct:
		for i := 0; i < srcValue.NumField(); i++ {
			field := srcValue.Type().Field(i)
			if field.IsExported() {
				k := toSnakeCase(field.Name)
				vars[k] = srcValue.Field(i).Interface()
			}
		}
	case reflect.Map:
		for _, key := range srcValue.MapKeys() {
			val := srcValue.MapIndex(key)
			k := toSnakeCase(fmt.Sprint(key.Interface()))
			vars[k] = val.Interface()
		}
	default:
		return errors.New("source must be a struct or a map")
	}

	return nil
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func toSnakeCase(s string) string {
	if s == "_" {
		return s
	}

	// Convert from kebab-case to snake_case
	s = strings.ReplaceAll(s, "-", "_")

	// Split by underscores to preserve them in the output
	underscoreBlocks := strings.Split(s, "_")
	processedBlocks := make([]string, 0, len(underscoreBlocks))

	for _, block := range underscoreBlocks {
		if block == "" {
			// Preserve empty blocks (consecutive underscores)
			processedBlocks = append(processedBlocks, "")
			continue
		}

		// Split camelCase words
		words := camelcase.Split(block)
		elems := make([]string, 0, len(words))

		for _, w := range words {
			if w != "" {
				if isDigits(w) {
					// If the word is digits-only, append it to the previous word
					if len(elems) > 0 {
						lastIndex := len(elems) - 1
						elems[lastIndex] = elems[lastIndex] + w
					} else {
						elems = append(elems, w)
					}
				} else {
					elems = append(elems, strings.ToLower(w))
				}
			}
		}
		// Join the processed words without underscores
		processedBlock := strings.Join(elems, "_")
		processedBlocks = append(processedBlocks, processedBlock)
	}

	// Rejoin the blocks with underscores
	return strings.Join(processedBlocks, "_")
}

type decodeHookFunc func(from reflect.Value, to reflect.Value) (any, error)

var decodeHook = composeDecodeHookFunc(
	decodeValToSlice,
	decodeStrToReader,
	decodeStrToBool,
	decodeStrToDuration,
	decodeStrToNum, // order matters, basic types should be decoded last
)

func decodeValToSlice(from reflect.Value, to reflect.Value) (any, error) {
	// if "from" has a type T and "to" is a value of type []T, append "from" to "to"
	if to.Kind() != reflect.Slice {
		return from.Interface(), nil
	}

	// Get the element type of the slice "to"
	elemType := to.Type().Elem()

	// Special case: if 'from' is a slice and 'to' is []any, iterate and append elements
	if from.Kind() == reflect.Slice && elemType.Kind() == reflect.Interface && elemType.NumMethod() == 0 {
		newSlice := to
		for i := 0; i < from.Len(); i++ {
			newSlice = reflect.Append(newSlice, from.Index(i))
		}
		return newSlice.Interface(), nil
	}

	// Check if "from" can be appended to "to"
	if !from.Type().AssignableTo(elemType) {
		return from.Interface(), nil
	}

	// Append "from" to "to"
	to = reflect.Append(to, from)

	// Return the modified slice
	return to.Interface(), nil
}

func decodeStrToNum(from reflect.Value, to reflect.Value) (any, error) {
	if from.Kind() != reflect.String {
		return from.Interface(), nil
	}
	str := from.String()
	var num any
	var err error
	switch to.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		num, err = strconv.ParseInt(str, 10, 64)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		num, err = strconv.ParseUint(str, 10, 64)
	case reflect.Float32, reflect.Float64:
		num, err = strconv.ParseFloat(str, 64)
	default:
		return from.Interface(), nil
	}
	if err != nil {
		return nil, err
	}
	return reflect.ValueOf(num).Convert(to.Type()).Interface(), nil
}

func decodeStrToBool(from reflect.Value, to reflect.Value) (any, error) {
	if from.Kind() != reflect.String || to.Kind() != reflect.Bool {
		return from.Interface(), nil
	}
	if from.String() != "" {
		return nil, fmt.Errorf("string value %q cannot be converted to bool, use ${true|false} "+
			"syntax instead", from.String())
	}
	return true, nil
}

func decodeStrToDuration(from reflect.Value, to reflect.Value) (any, error) {
	if from.Kind() != reflect.String || to.Type() != reflect.TypeOf(time.Duration(0)) {
		return from.Interface(), nil
	}
	return time.ParseDuration(from.String())
}

func decodeStrToReader(from reflect.Value, to reflect.Value) (any, error) {
	if from.Kind() != reflect.String || to.Type() != reflect.TypeOf((*io.Reader)(nil)).Elem() {
		return from.Interface(), nil
	}
	return strings.NewReader(from.String()), nil
}

func composeDecodeHookFunc(fns ...decodeHookFunc) decodeHookFunc {
	return func(f reflect.Value, t reflect.Value) (any, error) {
		for _, fn := range fns {
			if data, err := fn(f, t); err != nil {
				return nil, err
			} else {
				f = reflect.ValueOf(data)
			}
		}
		return f.Interface(), nil
	}
}
