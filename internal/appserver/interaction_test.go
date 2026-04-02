package appserver

import "testing"

func TestCloneJSONMapPreservesEmptyNestedObjects(t *testing.T) {
	cloned := cloneJSONMap(map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	})

	if cloned == nil {
		t.Fatal("expected cloned map")
	}
	properties, ok := cloned["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected properties to stay an object, got %#v", cloned["properties"])
	}
	if len(properties) != 0 {
		t.Fatalf("expected properties to stay empty, got %#v", properties)
	}
	if cloneJSONMap(nil) != nil {
		t.Fatal("expected nil map clone to stay nil")
	}
}
