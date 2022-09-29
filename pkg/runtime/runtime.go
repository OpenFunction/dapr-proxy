package runtime

import (
	"bytes"
	"encoding/json"
	nethttp "net/http"

	"github.com/OpenFunction/dapr-proxy/pkg/grpc"
	"github.com/OpenFunction/dapr-proxy/pkg/http"
	"github.com/OpenFunction/dapr-proxy/pkg/utils"
	ofctx "github.com/OpenFunction/functions-framework-go/context"
	"github.com/dapr/components-contrib/contenttype"
	"github.com/dapr/dapr/pkg/channel"
	invoke "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/modes"
	pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/runtime"
	"github.com/dapr/go-sdk/service/common"
	"github.com/pkg/errors"
)

type Config struct {
	Protocol   runtime.Protocol
	Host       string
	Port       int
	Mode       modes.DaprMode
	sslEnabled bool
}

type Runtime struct {
	config      *Config
	ctx         *ofctx.FunctionContext
	grpc        *grpc.Manager
	funcChannel channel.AppChannel
}

func NewFuncRuntime(config *Config, ctx *ofctx.FunctionContext) *Runtime {
	return &Runtime{
		config: config,
		ctx:    ctx,
	}
}

func (r *Runtime) CreateFuncChannel() error {
	switch r.config.Protocol {
	case runtime.HTTPProtocol:
		funcChannel, err := http.CreateHTTPChannel(r.config.Host, r.config.Port, r.config.sslEnabled)
		if err != nil {
			return errors.Errorf("cannot create app channel for protocol %s", string(r.config.Protocol))
		}
		r.funcChannel = funcChannel
	case runtime.GRPCProtocol:
		r.grpc = grpc.NewGRPCManager(r.config.Host, r.config.Port, r.config.sslEnabled)
		r.grpc.StartEndpointsDetection()
	default:
		return errors.Errorf("cannot create app channel for protocol %s", string(r.config.Protocol))
	}
	return nil
}

func (r *Runtime) OnBindingEvent(ctx ofctx.Context, event *common.BindingEvent) ([]byte, error) {
	var function func(ctx ofctx.Context, event *common.BindingEvent) ([]byte, error)
	switch r.config.Protocol {
	case runtime.HTTPProtocol:
		function = r.onBindingEventHTTP
	case runtime.GRPCProtocol:
		function = r.onBindingEventGRPC
	}
	return function(ctx, event)
}

func (r *Runtime) OnTopicEvent(ctx ofctx.Context, event *common.TopicEvent) error {
	var function func(ctx ofctx.Context, event *common.TopicEvent) error
	switch r.config.Protocol {
	case runtime.HTTPProtocol:
		function = r.onTopicEventHTTP
	case runtime.GRPCProtocol:
		function = r.onTopicEventGRPC
	}
	return function(ctx, event)
}

func (r *Runtime) onBindingEventHTTP(ctx ofctx.Context, event *common.BindingEvent) ([]byte, error) {
	path, _ := utils.GetComponentName(r.ctx)
	req := invoke.NewInvokeMethodRequest(path)
	req.WithHTTPExtension(nethttp.MethodPost, "")
	req.WithRawData(event.Data, invoke.JSONContentType)

	reqMetadata := map[string][]string{}
	for k, v := range event.Metadata {
		reqMetadata[k] = []string{v}
	}
	req.WithMetadata(reqMetadata)

	resp, err := r.funcChannel.InvokeMethod(ctx.GetNativeContext(), req)
	if err != nil {
		return nil, errors.Errorf("Error sending topic event to function: %s", err)
	}

	if resp != nil && resp.Status().Code != nethttp.StatusOK {
		return nil, errors.Errorf("Error sending binding event to function, status %d", resp.Status().Code)
	}
	_, data := resp.RawData()
	return data, nil
}

func (r *Runtime) onBindingEventGRPC(ctx ofctx.Context, bindingEvent *common.BindingEvent) ([]byte, error) {
	conn, release, err := r.grpc.GetGRPCConnection()
	defer release()
	if err != nil {
		return nil, err
	}
	client := pb.NewAppCallbackClient(conn)
	bindingName, _ := utils.GetComponentName(r.ctx)
	req := &pb.BindingEventRequest{
		Name:     bindingName,
		Data:     bindingEvent.Data,
		Metadata: bindingEvent.Metadata,
	}
	if resp, err := client.OnBindingEvent(ctx.GetNativeContext(), req); err != nil {
		return nil, errors.Errorf("Error sending binding event to function: %s", err)
	} else {
		return resp.Data, nil
	}
}

func (r *Runtime) onTopicEventHTTP(ctx ofctx.Context, event *common.TopicEvent) error {
	pubsubName, _ := utils.GetComponentName(r.ctx)
	path, _ := utils.GetTopicEventPath(r.ctx)
	req := invoke.NewInvokeMethodRequest(path)
	req.WithHTTPExtension(nethttp.MethodPost, "")

	bodyBytes := new(bytes.Buffer)
	if err := json.NewEncoder(bodyBytes).Encode(event); err != nil {
		return errors.Errorf("Error sending topic event to function: %s", err)
	}
	req.WithRawData(bodyBytes.Bytes(), contenttype.CloudEventContentType)

	metadata := make(map[string]string, 1)
	metadata["pubsubName"] = pubsubName
	req.WithCustomHTTPMetadata(metadata)

	resp, err := r.funcChannel.InvokeMethod(ctx.GetNativeContext(), req)
	if err != nil {
		return errors.Errorf("Error sending topic event to function: %s", err)
	}

	if resp != nil && resp.Status().Code != nethttp.StatusOK {
		return errors.Errorf("Error sending topic event to function, status %d", resp.Status().Code)
	}
	return nil
}

func (r *Runtime) onTopicEventGRPC(ctx ofctx.Context, event *common.TopicEvent) error {
	conn, release, err := r.grpc.GetGRPCConnection()
	defer release()
	if err != nil {
		return err
	}
	client := pb.NewAppCallbackClient(conn)
	path, _ := utils.GetTopicEventPath(r.ctx)
	req := &pb.TopicEventRequest{
		Id:              event.ID,
		Source:          event.Source,
		Type:            event.Type,
		SpecVersion:     event.SpecVersion,
		DataContentType: event.DataContentType,
		Data:            event.RawData,
		Topic:           event.Topic,
		PubsubName:      event.PubsubName,
		Path:            path,
	}

	if _, err := client.OnTopicEvent(ctx.GetNativeContext(), req); err != nil {
		return errors.Errorf("Error sending topic event to function: %s", err)
	}
	return nil
}
