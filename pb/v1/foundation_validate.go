package v1

import (
	"fmt"
	"unicode/utf8"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// validateClosed checks every nested message before language decoders can lose
// fields on a subsequent round trip. Structural validators add semantic rules.
func validateClosed(x proto.Message) error {
	if x == nil || !x.ProtoReflect().IsValid() {
		return fmt.Errorf("message is nil")
	}
	var walk func(protoreflect.Message) error
	walk = func(m protoreflect.Message) error {
		if err := checkNoUnknown(m, string(m.Descriptor().FullName())); err != nil {
			return err
		}
		if err := checkStringsUTF8(m, string(m.Descriptor().FullName())); err != nil {
			return err
		}
		var result error
		m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
			if fd.IsMap() {
				return true
			}
			if fd.Kind() != protoreflect.MessageKind {
				return true
			}
			if fd.IsList() {
				for i := 0; i < v.List().Len(); i++ {
					if result = walk(v.List().Get(i).Message()); result != nil {
						return false
					}
				}
			} else {
				result = walk(v.Message())
			}
			return result == nil
		})
		return result
	}
	return walk(x.ProtoReflect())
}

func (x *OutputFormat) Validate() error {
	if err := validateClosed(x); err != nil {
		return err
	}
	switch x.Mode {
	case OutputFormat_TEXT, OutputFormat_JSON_OBJECT:
		if x.Name != "" || len(x.SchemaJson) != 0 || x.Strict != nil {
			return fmt.Errorf("text/object output format cannot carry schema options")
		}
	case OutputFormat_JSON_SCHEMA:
		if x.Name == "" {
			return fmt.Errorf("JSON schema output requires a name")
		}
		if err := validateJSONObject(x.SchemaJson); err != nil {
			return fmt.Errorf("output schema: %w", err)
		}
	default:
		return fmt.Errorf("unknown output format mode")
	}
	return nil
}

func (x *SyntheticResponse) Validate() error {
	if err := validateClosed(x); err != nil {
		return err
	}
	if x.Message == nil || len(x.Message.Blocks) == 0 {
		return fmt.Errorf("synthetic response requires at least one block")
	}
	if err := x.Message.Validate(); err != nil {
		return err
	}
	hasTools := false
	for i, block := range x.Message.Blocks {
		if block == nil || block.Kind == nil {
			return fmt.Errorf("synthetic response block %d has no kind", i)
		}
		switch kind := block.Kind.(type) {
		case *ResponseBlock_Text:
			if kind == nil || kind.Text == nil {
				return fmt.Errorf("synthetic response text block %d is nil", i)
			}
		case *ResponseBlock_ToolCall:
			if kind == nil || kind.ToolCall == nil {
				return fmt.Errorf("synthetic response tool block %d is nil", i)
			}
			call := kind.ToolCall
			hasTools = true
			if call.Signature != "" || call.Id != "" {
				return fmt.Errorf("synthetic tool IDs and signatures are host-owned")
			}
			if err := validateJSONObject(call.ArgumentsJson); err != nil {
				return err
			}
		default:
			return fmt.Errorf("synthetic response block %d has an unsupported arm", i)
		}
	}
	if (!hasTools && x.FinishReason != "stop") || (hasTools && x.FinishReason != "tool_calls") {
		return fmt.Errorf("synthetic finish reason must agree with tool-call presence")
	}
	return nil
}

func (x *ResourceInfoArgs) Validate() error {
	if err := validateClosed(x); err != nil {
		return err
	}
	switch x.Kind {
	case "file":
		return validateLogicalPath("resource", x.Name, false)
	case "credential", "http", "model", "pricing", "cache_policy":
		if err := validateSlot("resource", x.Name); err != nil {
			return err
		}
		return nil
	default:
		return fmt.Errorf("unknown resource kind")
	}
}
func (x *ResourceInfo) Validate() error {
	if err := validateClosed(x); err != nil {
		return err
	}
	return (&ResourceInfoArgs{Kind: x.Kind, Name: x.Name}).Validate()
}
func (x *StateValue) Validate() error {
	if err := validateClosed(x); err != nil {
		return err
	}
	if x.Version == "" {
		return fmt.Errorf("state version is required")
	}
	return nil
}
func (x *StateCompareAndSetArgs) Validate() error {
	if err := validateClosed(x); err != nil {
		return err
	}
	if x.Key == "" {
		return fmt.Errorf("state key is required")
	}
	if x.ExpectedVersion != nil && *x.ExpectedVersion == "" {
		return fmt.Errorf("expected state version must be non-empty")
	}
	return nil
}
func (x *StateCompareAndDeleteArgs) Validate() error {
	if err := validateClosed(x); err != nil {
		return err
	}
	if x.Key == "" || x.ExpectedVersion == "" {
		return fmt.Errorf("state key and expected version are required")
	}
	return nil
}
func (x *StateMutationResult) Validate() error {
	if err := validateClosed(x); err != nil {
		return err
	}
	if x.Version != nil && (!x.Applied || *x.Version == "") {
		return fmt.Errorf("only an applied set has a non-empty version")
	}
	return nil
}
func (x *StateScanArgs) Validate() error {
	if err := validateClosed(x); err != nil {
		return err
	}
	if x.Limit == 0 || x.Limit > 256 {
		return fmt.Errorf("state scan limit must be 1..256")
	}
	return nil
}
func (x *StateScanResult) Validate() error {
	if err := validateClosed(x); err != nil {
		return err
	}
	if len(x.Entries) > 256 {
		return fmt.Errorf("state scan exceeds page limit")
	}
	var last string
	for i, e := range x.Entries {
		if e == nil || e.Key == "" || (i > 0 && e.Key <= last) {
			return fmt.Errorf("state entries must have unique ordered keys")
		}
		if err := e.Value.Validate(); err != nil {
			return err
		}
		last = e.Key
	}
	return nil
}
func (x *CacheDeleteArgs) Validate() error {
	if x == nil || x.Key == "" || !utf8.ValidString(x.Key) {
		return fmt.Errorf("cache key must be non-empty UTF-8")
	}
	return validateClosed(x)
}
