package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	nethttp "net/http"
	"sync"

	ofctx "github.com/OpenFunction/functions-framework-go/context"
	"github.com/cenkalti/backoff/v4"
	"github.com/dapr/components-contrib/contenttype"
	"github.com/dapr/dapr/pkg/channel"
	invoke "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/modes"
	pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/runtime"
	"github.com/dapr/go-sdk/service/common"
	"github.com/pkg/errors"
	"k8s.io/klog/v2"

	"github.com/OpenFunction/dapr-proxy/pkg/grpc"
	"github.com/OpenFunction/dapr-proxy/pkg/http"
	"github.com/OpenFunction/dapr-proxy/pkg/utils"
)

type Config struct {
	Protocol      runtime.Protocol
	Host          string
	Port          int
	Mode          modes.DaprMode
	sslEnabled    bool
	MaxBufferSize int
}

type EventRequest struct {
	ctx *context.Context
	ofctx.EventRequest
}

type EventResponse struct {
	Data  []byte
	Error error
}

type ResponseMap struct {
	l *sync.RWMutex
	m map[*context.Context]*EventResponse
}

type Runtime struct {
	config      *Config
	ctx         *ofctx.FunctionContext
	grpc        *grpc.Manager
	funcChannel channel.AppChannel
	reqChan     chan *EventRequest
	respMap     *ResponseMap
}

func NewFuncRuntime(config *Config, ctx *ofctx.FunctionContext) *Runtime {
	lock := new(sync.RWMutex)
	return &Runtime{
		config:  config,
		ctx:     ctx,
		reqChan: make(chan *EventRequest, config.MaxBufferSize),
		respMap: &ResponseMap{
			l: lock,
			m: make(map[*context.Context]*EventResponse),
		},
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
		if r.ctx.Runtime == ofctx.Async {
			r.grpc.StartEndpointsDetection()
		}
	default:
		return errors.Errorf("cannot create app channel for protocol %s", string(r.config.Protocol))
	}
	return nil
}

func (r *Runtime) ProcessEvents() {
	for e := range r.reqChan {
		if e.BindingEvent != nil {
			var data []byte
			// Retry on connection error.
			err := backoff.Retry(func() error {
				var err error
				data, err = r.OnBindingEvent(e.ctx, e.BindingEvent)
				if err != nil {
					klog.V(4).Info(err)
					return err
				}
				return nil
			}, utils.NewExponentialBackOff())

			resp := EventResponse{
				Data:  data,
				Error: err,
			}
			r.respMap.l.Lock()
			r.respMap.m[e.ctx] = &resp
			r.respMap.l.Unlock()
		}

		if e.TopicEvent != nil {
			// Retry on connection error.
			err := backoff.Retry(func() error {
				var err error
				err = r.OnTopicEvent(e.ctx, e.TopicEvent)
				if err != nil {
					return err
				}
				return nil
			}, utils.NewExponentialBackOff())

			resp := EventResponse{
				Data:  nil,
				Error: err,
			}
			r.respMap.l.Lock()
			r.respMap.m[e.ctx] = &resp
			r.respMap.l.Unlock()
		}
	}
}

func (r *Runtime) EnqueueBindingEvent(ctx *context.Context, event *common.BindingEvent) {
	req := EventRequest{
		ctx:          ctx,
		EventRequest: ofctx.EventRequest{BindingEvent: event},
	}
	r.reqChan <- &req
}

func (r *Runtime) EnqueueTopicEvent(ctx *context.Context, event *common.TopicEvent) {
	req := EventRequest{
		ctx:          ctx,
		EventRequest: ofctx.EventRequest{TopicEvent: event},
	}
	r.reqChan <- &req
}

func (r *Runtime) GetEventResponse(ctx *context.Context) *EventResponse {
	defer r.respMap.l.Unlock()
	r.respMap.l.Lock()
	if resp, ok := r.respMap.m[ctx]; ok {
		delete(r.respMap.m, ctx)
		return resp
	}
	return nil
}

func (r *Runtime) OnBindingEvent(ctx *context.Context, event *common.BindingEvent) ([]byte, error) {
	var function func(ctx *context.Context, event *common.BindingEvent) ([]byte, error)
	switch r.config.Protocol {
	case runtime.HTTPProtocol:
		function = r.onBindingEventHTTP
	case runtime.GRPCProtocol:
		function = r.onBindingEventGRPC
	}
	return function(ctx, event)
}

func (r *Runtime) OnTopicEvent(ctx *context.Context, event *common.TopicEvent) error {
	var function func(ctx *context.Context, event *common.TopicEvent) error
	switch r.config.Protocol {
	case runtime.HTTPProtocol:
		function = r.onTopicEventHTTP
	case runtime.GRPCProtocol:
		function = r.onTopicEventGRPC
	}
	return function(ctx, event)
}

func (r *Runtime) onBindingEventHTTP(ctx *context.Context, event *common.BindingEvent) ([]byte, error) {
	path, _ := utils.GetComponentName(r.ctx)
	req := invoke.NewInvokeMethodRequest(path)
	req.WithHTTPExtension(nethttp.MethodPost, "")
	req.WithRawData(event.Data, invoke.JSONContentType)

	reqMetadata := map[string][]string{}
	for k, v := range event.Metadata {
		reqMetadata[k] = []string{v}
	}
	req.WithMetadata(reqMetadata)

	resp, err := r.funcChannel.InvokeMethod(*ctx, req)
	if err != nil {
		return nil, errors.Errorf("Error sending topic event to function: %s", err)
	}

	if resp != nil && resp.Status().Code != nethttp.StatusOK {
		return nil, errors.Errorf("Error sending binding event to function, status %d", resp.Status().Code)
	}
	_, data := resp.RawData()
	return data, nil
}

func (r *Runtime) onBindingEventGRPC(ctx *context.Context, bindingEvent *common.BindingEvent) ([]byte, error) {
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
	if resp, err := client.OnBindingEvent(*ctx, req); err != nil {
		return nil, errors.Errorf("Error sending binding event to function: %s", err)
	} else {
		return resp.Data, nil
	}
}

func (r *Runtime) onTopicEventHTTP(ctx *context.Context, event *common.TopicEvent) error {
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

	resp, err := r.funcChannel.InvokeMethod(*ctx, req)
	if err != nil {
		return errors.Errorf("Error sending topic event to function: %s", err)
	}

	if resp != nil && resp.Status().Code != nethttp.StatusOK {
		return errors.Errorf("Error sending topic event to function, status %d", resp.Status().Code)
	}
	return nil
}

func (r *Runtime) onTopicEventGRPC(ctx *context.Context, event *common.TopicEvent) error {
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

	if _, err := client.OnTopicEvent(*ctx, req); err != nil {
		return errors.Errorf("Error sending topic event to function: %s", err)
	}
	return nil
}
