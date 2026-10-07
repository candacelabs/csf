// Copyright 2026 Candace Labs

package csf

import (
	"context"
	"errors"

	dispatchv1 "github.com/candacelabs/csf/proto/candace/dispatch/v1"
)

// ErrDispatchUnavailable reports a dispatch operation on a host that mounted
// no dispatch service.
var ErrDispatchUnavailable = errors.New("dispatch is not mounted in this host")

// IDispatch is the slice graph capability behind the generated dispatch
// operations: the service in candace/services/dispatch that ranks slices and
// dispatches the frontier onto this process's agent harness. The transport
// adapter delegates to it and owns no slice of its own.
//
//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=dispatch.go -destination=internal/mocks/dispatch.gen.go -package=csfmocks
type IDispatch interface {
	Enqueue(ctx context.Context, request *dispatchv1.EnqueueSliceRequest) (*dispatchv1.EnqueueSliceResponse, error)
	DeclareIntent(ctx context.Context, request *dispatchv1.DeclareIntentRequest) (*dispatchv1.DeclareIntentResponse, error)
	Route(ctx context.Context, request *dispatchv1.RouteMessageRequest) (*dispatchv1.RouteMessageResponse, error)
	Frontier(ctx context.Context, request *dispatchv1.GetFrontierRequest) (*dispatchv1.GetFrontierResponse, error)
	List(ctx context.Context, request *dispatchv1.ListSlicesRequest) (*dispatchv1.ListSlicesResponse, error)
	Reprioritize(ctx context.Context, request *dispatchv1.ReprioritizeRequest) (*dispatchv1.ReprioritizeResponse, error)
	MarkMerged(ctx context.Context, request *dispatchv1.MarkSliceMergedRequest) (*dispatchv1.MarkSliceMergedResponse, error)
}

// WithDispatch mounts the slice graph behind the generated dispatch
// operations.
func WithDispatch(dispatch IDispatch) Option {
	return func(service *Service) { service.dispatch = dispatch }
}

func (service *Service) dispatchOrUnavailable(ctx context.Context) (IDispatch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if service.dispatch == nil {
		return nil, ErrDispatchUnavailable
	}
	return service.dispatch, nil
}

// EnqueueSlice admits one slice with its edges, touch-set and provenance.
func (service *Service) EnqueueSlice(ctx context.Context, request *dispatchv1.EnqueueSliceRequest) (*dispatchv1.EnqueueSliceResponse, error) {
	dispatch, err := service.dispatchOrUnavailable(ctx)
	if err != nil {
		return nil, err
	}
	return dispatch.Enqueue(ctx, request)
}

// DeclareIntent records a typed intent and attaches it to the slices it
// routes to.
func (service *Service) DeclareIntent(ctx context.Context, request *dispatchv1.DeclareIntentRequest) (*dispatchv1.DeclareIntentResponse, error) {
	dispatch, err := service.dispatchOrUnavailable(ctx)
	if err != nil {
		return nil, err
	}
	return dispatch.DeclareIntent(ctx, request)
}

// RouteMessage steers, queues, creates or answers already done.
func (service *Service) RouteMessage(ctx context.Context, request *dispatchv1.RouteMessageRequest) (*dispatchv1.RouteMessageResponse, error) {
	dispatch, err := service.dispatchOrUnavailable(ctx)
	if err != nil {
		return nil, err
	}
	if err := dispatchv1.ValidateRouteMessageRequest(request); err != nil {
		return nil, errors.Join(ErrInvalidRequest, err)
	}
	return dispatch.Route(ctx, request)
}

// GetFrontier reports the slices ready to run, in dispatch order.
func (service *Service) GetFrontier(ctx context.Context, request *dispatchv1.GetFrontierRequest) (*dispatchv1.GetFrontierResponse, error) {
	dispatch, err := service.dispatchOrUnavailable(ctx)
	if err != nil {
		return nil, err
	}
	return dispatch.Frontier(ctx, request)
}

// ListSlices reports every slice with its priority breakdown.
func (service *Service) ListSlices(ctx context.Context, request *dispatchv1.ListSlicesRequest) (*dispatchv1.ListSlicesResponse, error) {
	dispatch, err := service.dispatchOrUnavailable(ctx)
	if err != nil {
		return nil, err
	}
	return dispatch.List(ctx, request)
}

// Reprioritize re-declares an existing intent.
func (service *Service) Reprioritize(ctx context.Context, request *dispatchv1.ReprioritizeRequest) (*dispatchv1.ReprioritizeResponse, error) {
	dispatch, err := service.dispatchOrUnavailable(ctx)
	if err != nil {
		return nil, err
	}
	return dispatch.Reprioritize(ctx, request)
}

// MarkSliceMerged records a merged pull request and releases the slices
// that depended on it.
func (service *Service) MarkSliceMerged(ctx context.Context, request *dispatchv1.MarkSliceMergedRequest) (*dispatchv1.MarkSliceMergedResponse, error) {
	dispatch, err := service.dispatchOrUnavailable(ctx)
	if err != nil {
		return nil, err
	}
	if err := dispatchv1.ValidateMarkSliceMergedRequest(request); err != nil {
		return nil, errors.Join(ErrInvalidRequest, err)
	}
	return dispatch.MarkMerged(ctx, request)
}
