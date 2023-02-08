package analyzer

import (
	"encoding/json"
	"fmt"

	"github.com/shiroyk/cloudcat/schema"
	"github.com/shiroyk/cloudcat/schema/parsers"
	"github.com/spf13/cast"
)

// Analyzer context analyzer
type Analyzer struct {
	FormatHandler FormatHandler
}

// NewAnalyzer returns a new analyzer
func NewAnalyzer() *Analyzer {
	return &Analyzer{
		FormatHandler: new(defaultFormatHandler),
	}
}

// ExecuteSchema execute a schema.Schema, returns the result
func (analyzer *Analyzer) ExecuteSchema(ctx *parsers.Context, s *schema.Schema, content string) any {
	defer func() {
		if r := recover(); r != nil {
			ctx.Logger().Error("analyzer error ", r.(error))
		}
	}()

	return analyzer.process(ctx, s, content)
}

func (analyzer *Analyzer) process(ctx *parsers.Context, s *schema.Schema, content any) any {
	switch s.Type {
	default:
		return nil
	case schema.StringType, schema.IntegerType, schema.NumberType, schema.BooleanType:
		return analyzer.processString(ctx, s, content)
	case schema.ObjectType:
		return analyzer.processObject(ctx, s, content)
	case schema.ArrayType:
		return analyzer.processArray(ctx, s, content)
	}
}

func (analyzer *Analyzer) processString(ctx *parsers.Context, s *schema.Schema, content any) any {
	var result any
	var err error
	if s.Type == schema.ArrayType {
		result, err = s.Rule.GetStrings(ctx, content)
		if err != nil {
			ctx.Logger().Error("process failed", err)
		}
	} else {
		result, err = s.Rule.GetString(ctx, content)
		if err != nil {
			ctx.Logger().Error("process failed", err)
		}

		if s.Type != schema.StringType {
			if result, err = analyzer.FormatHandler.Format(result, s.Type); err != nil {
				ctx.Logger().Error("format failed", err)
			}
		}
	}

	if s.Format != "" {
		if result, err = analyzer.FormatHandler.Format(result, s.Format); err != nil {
			ctx.Logger().Error("format failed", err)
		}
	}

	return result
}

func (analyzer *Analyzer) processObject(ctx *parsers.Context, s *schema.Schema, content any) any {
	if s.Properties != nil {
		element := analyzer.processInit(ctx, s, content)[0]
		object := make(map[string]any, len(s.Properties))

		for field, s := range s.Properties {
			object[field] = analyzer.process(ctx, &s, element)
		}

		return object
	} else if s.Rule != nil {
		return analyzer.processString(ctx, s.CloneWithType(schema.ObjectType), content)
	}

	return nil
}

func (analyzer *Analyzer) processArray(ctx *parsers.Context, s *schema.Schema, content any) any {
	if s.Properties != nil {
		elements := analyzer.processInit(ctx, s, content)
		array := make([]any, len(elements))

		for i, item := range elements {
			s := schema.NewSchema(schema.ObjectType).SetProperty(s.Properties)
			array[i] = analyzer.processObject(ctx, s, item)
		}

		return array
	} else if s.Rule != nil {
		return analyzer.processString(ctx, s.CloneWithType(schema.ArrayType), content)
	}

	return nil
}

func (analyzer *Analyzer) processInit(ctx *parsers.Context, s *schema.Schema, content any) []string {
	if s.Init == nil || len(s.Init) == 0 {
		switch data := content.(type) {
		case []string, nil:
			return data.([]string)
		case string:
			return []string{data}
		default:
			ctx.Logger().Error("process init failed", fmt.Errorf("unexpected content type %T", content))
			return nil
		}
	}

	if s.Type == schema.ArrayType {
		elements, err := s.Init.GetElements(ctx, content)
		if err != nil {
			ctx.Logger().Error("process init failed", err)
		}
		return elements
	}

	element, err := s.Init.GetElement(ctx, content)
	if err != nil {
		ctx.Logger().Error("process init failed", err)
	}
	return []string{element}
}

// FormatHandler schema property formatter
type FormatHandler interface {
	// Format the data to the given schema.SchemaType
	Format(data any, format schema.Type) (any, error)
}

type defaultFormatHandler struct{}

// Format the data to the given schema.SchemaType
func (f defaultFormatHandler) Format(data any, format schema.Type) (any, error) {
	switch data := data.(type) {
	case string:
		switch format {
		case schema.StringType:
			return data, nil
		case schema.IntegerType:
			return cast.ToIntE(data)
		case schema.NumberType:
			return cast.ToFloat64E(data)
		case schema.BooleanType:
			return cast.ToBoolE(data)
		case schema.ArrayType:
			slice := make([]any, 0)
			if err := json.Unmarshal([]byte(data), &slice); err != nil {
				return nil, err
			}
			return slice, nil
		case schema.ObjectType:
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
