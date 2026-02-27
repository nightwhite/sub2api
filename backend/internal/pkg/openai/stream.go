package openai

import "errors"

// ParseStreamParam parses the OpenAI request stream flag.
// Defaults to false when the field is missing.
func ParseStreamParam(reqBody map[string]any) (bool, error) {
	if reqBody == nil {
		return false, nil
	}
	rawStream, hasStream := reqBody["stream"]
	if !hasStream {
		return false, nil
	}
	streamValue, ok := rawStream.(bool)
	if !ok {
		return false, errors.New("stream must be a boolean")
	}
	return streamValue, nil
}
