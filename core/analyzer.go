package core

import (
	"encoding/json"
	"fmt"
	"runtime/debug"
	"strings"
	"sync/atomic"

	"github.com/shiroyk/cloudcat/plugin"
	"github.com/spf13/cast"
	"golang.org/x/exp/slog"
)

var attr = slog.String("type", "analyze")

var formatter atomic.Value

func init() {
	formatter.Store(new(defaultFormatHandler))
}

// SetFormatter set the formatter
func SetFormatter(formatHandler FormatHandler) {
	formatter.Store(formatHandler)
}

// GetFormatter get the formatter
func GetFormatter() FormatHandler {
	return formatter.Load().(FormatHandler)
}

// Analyze analyze a schema.Schema, returns the result
func Analyze(ctx *plugin.Context, s *Schema, content string) any {
	defer func() {
		if r := recover(); r != nil {
			ctx.Logger().Error(fmt.Sprintf("analyze error %s", r),
				"stack", string(debug.Stack()), attr)
		}
	}()

	return analyze(ctx, s, content, "$")
}

// analyze execute the corresponding to analyze by schema.Type
func analyze(
	ctx *plugin.Context,
	s *Schema,
	content any,
	path string, // the path of properties
) any {
	switch s.Type {
	default:
		return nil
	case StringType, IntegerType, NumberType, BooleanType:
		return analyzeString(ctx, s, content, path)
	case ObjectType:
		return analyzeObject(ctx, s, content, path)
	case ArrayType:
		return analyzeArray(ctx, s, content, path)
	}
}

// analyzeString get string or slice string and convert it to the specified type.
// If the type is not schema.StringType then convert to the specified type.
//
//nolint:nakedret
func analyzeString(
	ctx *plugin.Context,
	s *Schema,
	content any,
	path string, // the path of properties
) (ret any) {
	var err error
	if s.Type == ArrayType { //nolint:nestif
		ret, err = s.Rule.GetStrings(ctx, content)
		if err != nil {
			ctx.Logger().Error(fmt.Sprintf("analyze %s failed", path), "error", err, attr)
			return
		}
		ctx.Logger().Debug("parse", "path", path, "result", ret, attr)
	} else {
		ret, err = s.Rule.GetString(ctx, content)
		if err != nil {
			ctx.Logger().Error(fmt.Sprintf("analyze %s failed", path), "error", err, attr)
			return
		}
		ctx.Logger().Debug("parse", "path", path, "result", ret, attr)

		if s.Type != StringType {
			ret, err = GetFormatter().Format(ret, s.Type)
			if err != nil {
				ctx.Logger().Error(fmt.Sprintf("format %s failed %v to %v",
					path, ret, s.Format), "error", err, attr)
				return
			}
			ctx.Logger().Debug("format", "path", path, "result", ret, attr)
		}
	}

	if s.Format != "" {
		ret, err = GetFormatter().Format(ret, s.Format)
		if err != nil {
			ctx.Logger().Error(fmt.Sprintf("format %s failed %v to %v",
				path, ret, s.Format), "error", err, attr)
			return
		}
		ctx.Logger().Debug("format", "path", path, "result", ret, attr)
	}

	return
}

// analyzeObject get object.
// If properties is not empty, execute analyzeInit to get the object element then analyze properties.
// If rule is not empty, execute analyzeString to get object.
func analyzeObject(
	ctx *plugin.Context,
	s *Schema,
	content any,
	path string, // the path of properties
) (ret any) {
	if s.Properties != nil {
		element := analyzeInit(ctx, s, content, path)
		if len(element) == 0 {
			return
		}
		object := make(map[string]any, len(s.Properties))

		for field, fieldSchema := range s.Properties {
			object[field] = analyze(ctx, &fieldSchema, element[0], path+"."+field) //nolint:gosec
		}

		return object
	} else if s.Rule != nil {
		return analyzeString(ctx, s.CloneWithType(ObjectType), content, path)
	}

	return
}

// analyzeArray get array.
// If properties is not empty, execute analyzeInit to get the slice of elements then analyze properties.
// If rule is not empty, execute analyzeString to get array
func analyzeArray(
	ctx *plugin.Context,
	s *Schema,
	content any,
	path string, // the path of properties
) any {
	if s.Properties != nil {
		elements := analyzeInit(ctx, s, content, path)
		array := make([]any, len(elements))

		for i, item := range elements {
			newSchema := NewSchema(ObjectType).SetProperty(s.Properties)
			array[i] = analyzeObject(ctx, newSchema, item, fmt.Sprintf("%s.[%v]", path, i))
		}

		return array
	} else if s.Rule != nil {
		return analyzeString(ctx, s.CloneWithType(ArrayType), content, path)
	}

	return nil
}

// analyzeInit get type of object or array elements
func analyzeInit(
	ctx *plugin.Context,
	s *Schema,
	content any,
	path string, // the path of properties
) (ret []string) {
	if len(s.Init) == 0 {
		switch data := content.(type) {
		case []string:
			return data
		case string:
			return []string{data}
		default:
			ctx.Logger().Error(fmt.Sprintf("analyze %s init failed", path),
				"error", fmt.Errorf("unexpected content type %T", content), attr)
			return
		}
	}

	if s.Type == ArrayType {
		elements, err := s.Init.GetElements(ctx, content)
		if err != nil {
			ctx.Logger().Error(fmt.Sprintf("analyze %s init failed", path), "error", err, attr)
			return
		}
		ctx.Logger().Debug("init", "path", path, "result", strings.Join(elements, "\n"), attr)
		return elements
	}

	element, err := s.Init.GetElement(ctx, content)
	if err != nil {
		ctx.Logger().Error(fmt.Sprintf("analyze %s init failed", path), "error", err, attr)
		return
	}
	ctx.Logger().Debug("init", "path", path, "result", element)
	return []string{element}
}

// FormatHandler schema property formatter
type FormatHandler interface {
	// Format the data to the given Type
	Format(data any, format Type) (any, error)
}

type defaultFormatHandler struct{}

// Format the data to the given Type
func (f defaultFormatHandler) Format(data any, format Type) (any, error) {
	switch data := data.(type) {
	case string:
		switch format {
		case StringType:
			return data, nil
		case IntegerType:
			return cast.ToIntE(data)
		case NumberType:
			return cast.ToFloat64E(data)
		case BooleanType:
			return cast.ToBoolE(data)
		case ArrayType:
			slice := make([]any, 0)
			if err := json.Unmarshal([]byte(data), &slice); err != nil {
				return nil, err
			}
			return slice, nil
		case ObjectType:
			object := make(map[string]any, 0)
			if err := json.Unmarshal([]byte(data), &object); err != nil {
				return nil, err
			}
			return object, nil
		}
	case []string:
		slice := make([]any, len(data))
		for i, o := range data {
			slice[i], _ = f.Format(o, format)
		}
		return slice, nil
	case map[string]any:
		maps := make(map[string]any, len(data))
		for k, v := range data {
			maps[k], _ = f.Format(v, format)
		}
		return maps, nil
	}
	return data, fmt.Errorf("unexpected type %T", data)
}
