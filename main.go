package main

import (
	"context"
	"flag"
	"strconv"
	"time"

	proxyruntime "github.com/OpenFunction/dapr-proxy/pkg/runtime"
	"github.com/OpenFunction/dapr-proxy/pkg/utils"
	ofctx "github.com/OpenFunction/functions-framework-go/context"
	"github.com/OpenFunction/functions-framework-go/framework"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/runtime"
	"github.com/pkg/errors"
	"k8s.io/klog/v2"
)

const (
	defaultAppProtocol = "grpc"
	protocolEnvVar     = "APP_PROTOCOL"
	debugEnvVar        = "DEBUG"
)

var (
	FuncRuntime *proxyruntime.Runtime
)

func main() {
	debugVal := utils.GetEnvVar(debugEnvVar, "false")
	debug, _ := strconv.ParseBool(debugVal)
	if debug {
		klog.InitFlags(nil)
		flag.Set("v", "4")
		flag.Parse()
	}

	ctx := context.Background()
	fwk, err := framework.NewFramework()
	if err != nil {
		klog.Exit(err)
	}

	funcContext := utils.GetFuncContext(fwk)

	host := utils.GetFuncHost(funcContext)
	port, _ := strconv.Atoi(funcContext.GetPort())
	protocol := utils.GetEnvVar(protocolEnvVar, defaultAppProtocol)
	config := &proxyruntime.Config{
		Protocol: runtime.Protocol(protocol),
		Host:     host,
		Port:     port,
		Mode:     modes.KubernetesMode,
	}

	FuncRuntime = proxyruntime.NewFuncRuntime(config, funcContext)
	if err := FuncRuntime.CreateFuncChannel(); err != nil {
		klog.Exit(err)
	}

	if err := fwk.Register(ctx, EventHandler); err != nil {
		klog.Exit(err)
	}

	if err := fwk.Start(ctx); err != nil {
		klog.Exit(err)
	}
}

func EventHandler(ctx ofctx.Context, in []byte) (ofctx.Out, error) {
	start := time.Now()
	defer func() {
		elapsed := diag.ElapsedSince(start)
		klog.V(4).Infof("Input: %s - Event Forwarding Elapsed: %vms", ctx.GetInputName(), elapsed)
	}()

	// Forwarding BindingEvent
	bindingEvent := ctx.GetBindingEvent()
	if bindingEvent != nil {
		data, err := FuncRuntime.OnBindingEvent(ctx, bindingEvent)
		if err != nil {
			klog.Error(err)
			return ctx.ReturnOnInternalError(), err
		} else {
			out := new(ofctx.FunctionOut)
			out.WithData(data)
			out.WithCode(ofctx.Success)
			return out, nil
		}
	}

	// Forwarding TopicEvent
	topicEvent := ctx.GetTopicEvent()
	if topicEvent != nil {
		err := FuncRuntime.OnTopicEvent(ctx, topicEvent)
		if err != nil {
			klog.Error(err)
			return ctx.ReturnOnInternalError(), err
		} else {
			out := new(ofctx.FunctionOut)
			out.WithCode(ofctx.Success)
			return out, nil
		}
	}

	err := errors.New("Only Binding and Pubsub events are supported")
	return ctx.ReturnOnInternalError(), err
}
