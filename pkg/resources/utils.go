package resources

import (
	"context"
	"fmt"
	"io/ioutil"
	"reflect"
	"strings"

	"github.com/go-logr/logr"
	pkgerrors "github.com/pkg/errors"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/k11n/konstellation/pkg/utils/objects"
)

const (
	dateTimeFormat = "20060102-1504"
)

var (
	ErrNotFound = fmt.Errorf("the resource is not found")
	Break       = fmt.Errorf("")
	log         = logf.Log.WithName("resources")
)

func UpdateResource(kclient client.Client, object, owner metav1.Object, scheme *runtime.Scheme) (result controllerutil.OperationResult, err error) {
	return updateResource(kclient, object, owner, scheme, false)
}

func UpdateResourceWithMerge(kclient client.Client, object, owner metav1.Object, scheme *runtime.Scheme) (result controllerutil.OperationResult, err error) {
	return updateResource(kclient, object, owner, scheme, true)
}

// Create or update the resource
// only handles updates to Annotations, Labels, and Spec
// TODO: this is very core and need tests
func updateResource(kclient client.Client, object, owner metav1.Object, scheme *runtime.Scheme, merge bool) (controllerutil.OperationResult, error) {
	existingVal := reflect.New(reflect.TypeOf(object).Elem())
	existingObj := existingVal.Interface().(metav1.Object)
	existingObj.SetNamespace(object.GetNamespace())
	existingObj.SetName(object.GetName())
	existingRuntimeObj, ok := existingObj.(runtime.Object)
	if !ok {
		return controllerutil.OperationResultNone, fmt.Errorf("not a runtime Object")
	}

	key, err := client.ObjectKeyFromObject(existingRuntimeObj)
	if err != nil {
		return controllerutil.OperationResultNone, err
	}

	// create new if existing is not found
	if err := kclient.Get(context.TODO(), key, existingRuntimeObj); err != nil {
		if !errors.IsNotFound(err) {
			return controllerutil.OperationResultNone, err
		}
		if owner != nil && scheme != nil {
			if err = controllerutil.SetControllerReference(owner, object, scheme); err != nil {
				return controllerutil.OperationResultNone, err
			}
		}
		if err := kclient.Create(context.TODO(), object.(runtime.Object)); err != nil {
			return controllerutil.OperationResultNone, err
		}
		return controllerutil.OperationResultCreated, nil
	}

	changed := false
	if !apiequality.Semantic.DeepEqual(existingObj.GetAnnotations(), object.GetAnnotations()) {
		existingObj.SetAnnotations(object.GetAnnotations())
		changed = true
	}
	if !apiequality.Semantic.DeepEqual(existingObj.GetLabels(), object.GetLabels()) {
		existingObj.SetLabels(object.GetLabels())
		changed = true
	}

	// deep copy spec so we can apply and detect changes
	// particularly with using merge, it's difficult to know what's changed, so we'd have to apply
	// updates and confirm
	existingCopy := existingRuntimeObj.DeepCopyObject()

	existingSpec := existingVal.Elem().FieldByName("Spec")
	targetSpec := reflect.ValueOf(object).Elem().FieldByName("Spec")
	if merge {
		objects.MergeObject(existingSpec.Addr().Interface(), targetSpec.Addr().Interface())
	} else {
		existingSpec.Set(targetSpec)
	}
	copiedSpec := reflect.ValueOf(existingCopy).Elem().FieldByName("Spec")
	if !apiequality.Semantic.DeepEqual(existingSpec.Addr().Interface(), copiedSpec.Addr().Interface()) {
		//log.Info("changes detected", "old", copiedSpec.Addr().Interface(), "new", existingSpec.Addr().Interface())
		changed = true
	}

	// copy over status if available
	existingStatus := existingVal.Elem().FieldByName("Status")
	targetStatus := reflect.ValueOf(object).Elem().FieldByName("Status")
	if targetStatus.IsValid() {
		existingStatus.Set(targetStatus)
	}

	res := controllerutil.OperationResultNone
	if changed {
		if err := kclient.Update(context.TODO(), existingRuntimeObj); err != nil {
			return res, err
		}
		res = controllerutil.OperationResultUpdated
	}

	// use existing value
	reflect.ValueOf(object).Elem().Set(existingVal.Elem())
	return res, nil
}

func LogUpdates(log logr.Logger, op controllerutil.OperationResult, message string, keysAndValues ...interface{}) {
	if op == controllerutil.OperationResultNone {
		return
	}
	keysAndValues = append(keysAndValues, "op", op)
	log.Info(message, keysAndValues...)
}

/**
 * Helper function to iterate through all resources in a list
 */
func ForEach(kclient client.Client, listObj runtime.Object, eachFunc func(item interface{}) error, opts ...client.ListOption) error {
	shouldRun := true
	var contToken string
	ctx := context.Background()

	for shouldRun {
		shouldRun = false

		options := make([]client.ListOption, 0, len(opts)+2)
		options = append(options, opts...)
		options = append(options, client.Limit(20))
		if contToken != "" {
			options = append(options, client.Continue(contToken))
		}
		err := kclient.List(ctx, listObj, options...)
		if err != nil {
			return err
		}

		listVal := reflect.ValueOf(listObj).Elem()
		if listVal.IsZero() {
			return fmt.Errorf("list object is missing")
		}

		itemsField := listVal.FieldByName("Items")
		if itemsField.IsZero() {
			return fmt.Errorf("list object doesn't not contain Items field")
		}

		for i := 0; i < itemsField.Len(); i += 1 {
			item := itemsField.Index(i).Interface()
			if err = eachFunc(item); err != nil && err != Break {
				return err
			}
		}

		// find contToken
		listMeta := listVal.FieldByName("ListMeta")
		lm := listMeta.Interface().(metav1.ListMeta)
		contToken = lm.Continue
		if contToken != "" {
			shouldRun = true
		}
	}
	return nil
}

func ToEnvVar(s string) string {
	return strings.ToUpper(strings.ReplaceAll(s, "-", "_"))
}

func NewYAMLEncoder() runtime.Encoder {
	return json.NewSerializerWithOptions(json.DefaultMetaFactory, nil, nil,
		json.SerializerOptions{
			Yaml:   true,
			Pretty: true,
			Strict: false,
		})
}

func NewYAMLDecoder() runtime.Decoder {
	return clientgoscheme.Codecs.UniversalDeserializer()
}

func ReadObjectFromFile(decoder runtime.Decoder, filename string, obj runtime.Object) (res runtime.Object, err error) {
	content, err := ioutil.ReadFile(filename)
	if err != nil {
		return
	}
	res, _, err = decoder.Decode(content, nil, obj)
	if err != nil {
		return nil, pkgerrors.Wrapf(err, "could not read object from %s", filename)
	}

	return
}
