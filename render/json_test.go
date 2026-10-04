package render

import "encoding/json"

func jsonUnmarshal(p []byte, e *Event) error { return json.Unmarshal(p, e) }
