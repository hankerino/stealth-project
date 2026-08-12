package main

import (
	"context"
	"errors"

	orderv1 "github.com/hankerino/stealth-project/libs/proto/gen/order/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// grpcServer implements orderv1.OrderServiceServer over the shared core.
type grpcServer struct {
	orderv1.UnimplementedOrderServiceServer
	svc *orderService
}

func protoSideToString(s orderv1.Side) string {
	switch s {
	case orderv1.Side_SIDE_BUY:
		return SideBuy
	case orderv1.Side_SIDE_SELL:
		return SideSell
	default:
		return ""
	}
}

func protoTIFToString(t orderv1.TimeInForce) string {
	switch t {
	case orderv1.TimeInForce_TIME_IN_FORCE_GTC:
		return TIFGTC
	case orderv1.TimeInForce_TIME_IN_FORCE_IOC:
		return TIFIOC
	case orderv1.TimeInForce_TIME_IN_FORCE_FOK:
		return TIFFOK
	default:
		return ""
	}
}

func statusToProto(s string) orderv1.OrderStatus {
	switch s {
	case "OPEN":
		return orderv1.OrderStatus_ORDER_STATUS_OPEN
	case "PARTIALLY_FILLED":
		return orderv1.OrderStatus_ORDER_STATUS_PARTIALLY_FILLED
	case "FILLED":
		return orderv1.OrderStatus_ORDER_STATUS_FILLED
	case "CANCELLED":
		return orderv1.OrderStatus_ORDER_STATUS_CANCELLED
	case "REJECTED":
		return orderv1.OrderStatus_ORDER_STATUS_REJECTED
	case "EXPIRED":
		return orderv1.OrderStatus_ORDER_STATUS_EXPIRED
	default:
		return orderv1.OrderStatus_ORDER_STATUS_UNSPECIFIED
	}
}

func (g *grpcServer) SubmitOrder(ctx context.Context, req *orderv1.SubmitOrderRequest) (*orderv1.SubmitOrderResponse, error) {
	o, err := g.svc.placeOrder(ctx, &OrderInput{
		UserID:      req.GetUserId(),
		GPUType:     req.GetGpuType(),
		Region:      req.GetRegion(),
		Side:        protoSideToString(req.GetSide()),
		PriceCents:  req.GetPriceCents(),
		Quantity:    req.GetQuantity(),
		TimeInForce: protoTIFToString(req.GetTimeInForce()),
	})
	if err != nil {
		if o != nil {
			// Persisted but event publish failed — still report success.
			return &orderv1.SubmitOrderResponse{OrderId: o.ID, Status: statusToProto(o.Status)}, nil
		}
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &orderv1.SubmitOrderResponse{OrderId: o.ID, Status: statusToProto(o.Status)}, nil
}

func (g *grpcServer) CancelOrder(ctx context.Context, req *orderv1.CancelOrderRequest) (*orderv1.CancelOrderResponse, error) {
	o, err := g.svc.cancelOrder(ctx, req.GetUserId(), req.GetOrderId())
	if errors.Is(err, errNotFound) {
		return nil, status.Error(codes.NotFound, "order not found or not cancellable")
	}
	if err != nil && o == nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &orderv1.CancelOrderResponse{OrderId: o.ID, Status: statusToProto(o.Status)}, nil
}

func (g *grpcServer) GetOrder(ctx context.Context, req *orderv1.GetOrderRequest) (*orderv1.GetOrderResponse, error) {
	o, err := g.svc.getOrder(ctx, req.GetOrderId())
	if errors.Is(err, errNotFound) {
		return nil, status.Error(codes.NotFound, "order not found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &orderv1.GetOrderResponse{
		OrderId:         o.ID,
		UserId:          o.UserID,
		GpuType:         o.GPUType,
		Region:          o.Region,
		Side:            sideToProto(o.Side),
		PriceCents:      o.PriceCents,
		Quantity:        o.Quantity,
		FilledQuantity:  o.FilledQuantity,
		Status:          statusToProto(o.Status),
		CreatedAtUnixMs: o.CreatedAt.UnixMilli(),
		UpdatedAtUnixMs: o.UpdatedAt.UnixMilli(),
	}, nil
}

func sideToProto(s string) orderv1.Side {
	switch s {
	case SideBuy:
		return orderv1.Side_SIDE_BUY
	case SideSell:
		return orderv1.Side_SIDE_SELL
	default:
		return orderv1.Side_SIDE_UNSPECIFIED
	}
}
