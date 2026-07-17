package static

// getStringFromMap safely extracts a string value from a map.
func getStringFromMap(m map[string]any, key string) string {
	if val, ok := m[key]; ok && val != nil {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}

// getInt64FromMap safely extracts an int64 value from a map, handling
// int, int64, and float64 types.
func getInt64FromMap(m map[string]any, key string) int64 {
	if val, ok := m[key]; ok && val != nil {
		switch v := val.(type) {
		case int64:
			return v
		case int:
			return int64(v)
		case float64:
			return int64(v)
		}
	}
	return -1
}
