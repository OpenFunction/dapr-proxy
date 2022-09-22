package utils

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"unsafe"

	ofctx "github.com/OpenFunction/functions-framework-go/context"
	"github.com/pkg/errors"
)

const FuncContextName = "funcContext"

func GetFuncContext(fwk interface{}) *ofctx.FunctionContext {
	v := reflect.ValueOf(fwk).Elem().FieldByName(FuncContextName)
	e := reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem()
	return e.Interface().(*ofctx.FunctionContext)
}

func GetFuncHost(ctx *ofctx.FunctionContext) string {
	name := ctx.GetName()
	namespace := ctx.GetPodNamespace()
	host := fmt.Sprintf("%s.%s.svc.cluster.local", name, namespace)
	return host
}

func GetComponentName(ctx *ofctx.FunctionContext) (string, error) {
	inputName := ctx.Event.InputName
	for key, input := range ctx.Inputs {
		if key == inputName {
			return input.ComponentName, nil
		}
	}
	err := errors.New("failed to get component name")
	return "", err
}

func GetTopicEventPath(ctx *ofctx.FunctionContext) (string, error) {
	inputName := ctx.Event.InputName
	for key, input := range ctx.Inputs {
		if key == inputName {
			return input.Uri, nil
		}
	}
	err := errors.New("failed to get topic event path")
	return "", err
}

func GetEnvVar(key, fallbackValue string) string {
	if val, ok := os.LookupEnv(key); ok {
		return strings.TrimSpace(val)
	}
	return fallbackValue
}
