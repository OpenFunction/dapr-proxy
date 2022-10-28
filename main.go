package main

import (
	"context"
	"flag"
	"strconv"
	"time"

	ofctx "github.com/OpenFunction/functions-framework-go/context"
	"github.com/OpenFunction/functions-framework-go/framework"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/runtime"
	"k8s.io/klog/v2"

	proxyruntime "github.com/OpenFunction/dapr-proxy/pkg/runtime"
	"github.com/OpenFunction/dapr-proxy/pkg/utils"
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
		Protocol:      runtime.Protocol(protocol),
		Host:          host,
		Port:          port,
		Mode:          modes.KubernetesMode,
		MaxBufferSize: 1000000,
	}

	FuncRuntime = proxyruntime.NewFuncRuntime(config, funcContext)
	if err := FuncRuntime.CreateFuncChannel(); err != nil {
		klog.Exit(err)
	}

	go FuncRuntime.ProcessEvents()

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
		pendingEventsCount := FuncRuntime.GetPendingEventsCount()
		klog.V(4).Infof("Input: %s - Pending Events Count: %v", ctx.GetInputName(), pendingEventsCount)
		klog.V(4).Infof("Input: %s - Event Forwarding Elapsed: %vms", ctx.GetInputName(), elapsed)
	}()

	c := ctx.GetNativeContext()
	respCh := make(chan *proxyruntime.EventResponse, 1)

	// Handle BindingEvent
	bindingEvent := ctx.GetBindingEvent()
	if bindingEvent != nil {
		event := proxyruntime.NewEvent(&c, bindingEvent, nil, respCh)
		FuncRuntime.EnqueueEvent(&event)
	}

	// Handle TopicEvent
	topicEvent := ctx.GetTopicEvent()
	if topicEvent != nil {
		event := proxyruntime.NewEvent(&c, nil, topicEvent, respCh)
		FuncRuntime.EnqueueEvent(&event)
	}

	resp := <-respCh
	if resp.Error != nil {
		klog.Error(resp.Error)
		return ctx.ReturnOnInternalError(), resp.Error
	} else {
		out := new(ofctx.FunctionOut)
		out.WithData(resp.Data)
		out.WithCode(ofctx.Success)
		return out, nil
	}
}
