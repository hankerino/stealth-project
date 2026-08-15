package main

// gRPC front for the risk service, implementing cte.risk.v1.RiskService.
// The order service calls CheckMargin before accepting a FUTURES order.
// Generated stubs (riskv1) are produced by protoc from
// libs/proto/risk/v1/risk.proto (see .github/workflows/risk.yml / Makefile).

import (
	"context"

	riskv1 "github.com/hankerino/stealth-project/libs/proto/gen/risk/v1"
)

type grpcServer struct {
	riskv1.UnimplementedRiskServiceServer
	svc *riskService
}

func sideString(s riskv1.Side) string {
	switch s {
	case riskv1.Side_SIDE_BUY:
		return SideBuy
	case riskv1.Side_SIDE_SELL:
		return SideSell
	default:
		return ""
	}
}

func kindString(k riskv1.OrderKind) string {
	switch k {
	case riskv1.OrderKind_ORDER_KIND_FUTURES:
		return KindFutures
	case riskv1.OrderKind_ORDER_KIND_SPOT:
		return KindSpot
	default:
		return ""
	}
}

func (g *grpcServer) CheckMargin(ctx context.Context, req *riskv1.CheckMarginRequest) (*riskv1.CheckMarginResponse, error) {
	res, err := g.svc.CheckMargin(ctx, MarginCheck{
		UserID:     req.GetUserId(),
		ContractID: req.GetContractId(),
		Symbol:     req.GetSymbol(),
		Kind:       kindString(req.GetOrderKind()),
		Side:       sideString(req.GetSide()),
		PriceCents: req.GetPriceCents(),
		Quantity:   req.GetQuantity(),
	})
	if err != nil {
		return nil, err
	}
	return &riskv1.CheckMarginResponse{
		Allowed:             res.Allowed,
		RequiredMarginCents: res.RequiredMarginCents,
		AvailableCents:      res.AvailableCents,
		ProjectedPosition:   res.ProjectedPosition,
		Reason:              res.Reason,
	}, nil
}

func (g *grpcServer) GetPosition(ctx context.Context, req *riskv1.GetPositionRequest) (*riskv1.GetPositionResponse, error) {
	pos, err := g.svc.GetPosition(ctx, req.GetUserId(), req.GetContractId())
	if err != nil {
		return nil, err
	}
	return &riskv1.GetPositionResponse{
		UserId:             pos.UserID,
		ContractId:         pos.ContractID,
		Symbol:             pos.Symbol,
		NetQuantity:        pos.NetQuantity,
		AvgEntryPriceCents: pos.AvgEntryPriceCents,
		MarginPostedCents:  pos.MarginPostedCents,
	}, nil
}
