package objects

import (
	"fmt"
	"reflect"

	"github.com/imdario/mergo"
	corev1 "k8s.io/api/core/v1"
)

var (
	transformers = newMergeTransformers()
)

// fill in non-empty fields from src to dest
func MergeObject(dest, src interface{}) {
	err := mergo.Merge(dest, src, mergo.WithTransformers(transformers), mergo.WithOverride)
	if err != nil {
		fmt.Printf("error merging: %v and %v, %v\n",
			dest, src, err)
	}
}

func MergeSlice(dest, src interface{}) {
	destVal := reflect.ValueOf(dest)
	srcVal := reflect.ValueOf(src)

	if destVal.Kind() != srcVal.Kind() {
		return
	}

	if destVal.Kind() != reflect.Ptr && destVal.Kind() != reflect.Interface {
		fmt.Printf("not an interface or pointer")
		return
	}

	if destVal.Elem().Kind() != srcVal.Elem().Kind() || destVal.Elem().Kind() != reflect.Slice {
		return
	}

	mergeSliceValue(destVal.Elem(), srcVal.Elem())
}

func mergeSliceValue(dst, src reflect.Value) error {
	if src.Len() != dst.Len() {
		// override it entirely
		dst.Set(src)
		return nil
	}
	// equal length.. so we'll merge underlying types
	for i := 0; i < src.Len(); i++ {
		srcVal := src.Index(i)
		dstVal := dst.Index(i)
		switch srcVal.Kind() {
		case reflect.Struct:
			dstPtr := reflect.New(dstVal.Type())
			dstPtr.Elem().Set(dstVal)
			MergeObject(dstPtr.Interface(), srcVal.Interface())
			dstVal.Set(dstPtr.Elem())
		default:
			dstVal.Set(srcVal)
		}
	}
	return nil
}

func mergeResourceRequirements(dst, src reflect.Value) error {
	// merge resource requirements differently
	dstRes := dst.Addr().Interface().(*corev1.ResourceRequirements)
	srcRes := src.Addr().Interface().(*corev1.ResourceRequirements)

	if srcRes.Requests != nil {
		if dstRes.Requests == nil {
			dstRes.Requests = make(corev1.ResourceList, len(srcRes.Requests))
		}
		for key, val := range srcRes.Requests {
			dstRes.Requests[key] = val
		}
	}

	for key, val := range srcRes.Limits {
		if dstRes.Limits == nil {
			dstRes.Limits = make(corev1.ResourceList, len(srcRes.Limits))
		}
		dstRes.Limits[key] = val
	}
	return nil
}

func newMergeTransformers() mergeTransformers {
	return mergeTransformers{
		mergeFuncs: map[reflect.Type]mergeFunc{
			reflect.TypeOf(corev1.ResourceRequirements{}): mergeResourceRequirements,
		},
	}
}

type mergeFunc func(dst, src reflect.Value) error

type mergeTransformers struct {
	// types that should be overridden
	mergeFuncs map[reflect.Type]mergeFunc
}

func (t mergeTransformers) Transformer(oType reflect.Type) func(dst, src reflect.Value) error {
	switch oType.Kind() {
	case reflect.Slice:
		return mergeSliceValue
	case reflect.Struct:
		return t.mergeFuncs[oType]
	}
	return nil
}
