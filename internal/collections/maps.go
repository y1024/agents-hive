package collections

// CloneStringMap 复制 string map；nil 输入返回 nil，空 map 返回空 map。
func CloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// CloneNonEmptyStringMap 复制 string map；nil 或空 map 返回 nil。
func CloneNonEmptyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	return CloneStringMap(in)
}

// CloneNonEmptyNestedStringMap 复制嵌套 string map，并跳过空子 map。
// 如果最终没有任何非空子 map，返回 nil。
func CloneNonEmptyNestedStringMap(in map[string]map[string]string) map[string]map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]map[string]string, len(in))
	for key, values := range in {
		if len(values) == 0 {
			continue
		}
		out[key] = CloneStringMap(values)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
