package context

import "reflect"

// IsNilCompleter 兼容 typed-nil：*llm.Client(nil) 包进接口后接口非 nil，需反射判断底层指针。
func IsNilCompleter(c Completer) bool {
	if c == nil {
		return true
	}
	v := reflect.ValueOf(c)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return v.IsNil()
	}
	return false
}
