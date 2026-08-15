package main

// gRPC client to the Risk service for pre-trade margin checks (Phase 2).
// The order service calls CheckMargin before accepting a FUTURES order.

import (
	"context"
	"time"

	riskv1 "github.com/hankerino/stealth-project/libs/proto/gen/risk/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// MarginReq is the order service's view of a margin check.
type MarginReq struct {
	UserID     string
	ContractID int64
	Symbol     string
	Kind       string // SPOT | FUTURES
	Side       string // BUY | SELL
	PriceCents int64
	Quantity   int64
}

// RiskChecker abstracts the Risk service so the order core is testable and so
// dev can run without Risk (nopRiskChecker allows everything).
type RiskChecker interface {
	CheckMargin(ctx context.Context, req MarginReq) (allowed bool, reason string, err error)
}

// nopRiskChecker allows all orders; used when RISK_ADDR is unset (dev).
type nopRiskChecker struct{}

func (nopRiskChecker) CheckMargin(context.Context, MarginReq) (bool, string, error) {
	return true, "", nil
}

// riskGRPCClient calls the Risk service over gRPC.
type riskGRPCClient struct {
	conn   *grpc.ClientConn
	client riskv1.RiskServiceClient
}

func newRiskGRPCClient(addr string) (*riskGRPCClient, error) {
	// Internal cluster traffic; TLS/mTLS is a follow-up (same cert story as Kafka).
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	return &riskGRPCClient{conn: conn, client: riskv1.NewRiskServiceClient(conn)}, nil
}

func kindToProto(kind string) riskv1.OrderKind {
	switch kind {
	case KindFutures:
		return riskv1.OrderKind_ORDER_KIND_FUTURES
	case KindSpot:
		return riskv1.OrderKind_ORDER_KIND_SPOT
	default:
		return riskv1.OrderKind_ORDER_KIND_UNSPECIFIED
	}
}

func riskSideToProto(side string) riskv1.Side {
	switch side {
	case SideBuy:
		return riskv1.Side_SIDE_BUY
	case SideSell:
		return riskv1.Side_SIDE_SELL
	default:
		return riskv1.Side_SIDE_UNSPECIFIED
	}
}

func (c *riskGRPCClient) CheckMargin(ctx context.Context, req MarginReq) (bool, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	resp, err := c.client.CheckMargin(ctx, &riskv1.CheckMarginRequest{
		UserId:     req.UserID,
		ContractId: req.ContractID,
		Symbol:     req.Symbol,
		OrderKind:  kindToProto(req.Kind),
		Side:       riskSideToProto(req.Side),
		PriceCents: req.PriceCents,
		Quantity:   req.Quantity,
	})
	if err != nil {
		return false, "", err
	}
	return resp.GetAllowed(), resp.GetReason(), nil
}

func (c *riskGRPCClient) Close() error { return c.conn.Close() }
