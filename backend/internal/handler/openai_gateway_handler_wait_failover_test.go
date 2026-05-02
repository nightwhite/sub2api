package handler

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"go.uber.org/zap"
)

type openAIAccountRepoStub struct {
	service.AccountRepository
	base        service.Account
	schedulable *atomic.Bool
}

func (r *openAIAccountRepoStub) GetByID(ctx context.Context, id int64) (*service.Account, error) {
	if r == nil || r.base.ID <= 0 {
		return nil, errors.New("account not found")
	}
	if id != r.base.ID {
		return nil, errors.New("account not found")
	}
	acc := r.base
	if r.schedulable != nil {
		acc.Schedulable = r.schedulable.Load()
	}
	return &acc, nil
}

func TestOpenAIGatewayHandler_AcquireResponsesAccountSlot_WaitTimeoutFailover(t *testing.T) {
	cache := &helperConcurrencyCacheStub{}
	concurrency := service.NewConcurrencyService(cache)
	helper := NewConcurrencyHelper(concurrency, SSEPingFormatNone, 0)

	account := service.Account{
		ID:          101,
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeOAuth,
		Status:      service.StatusActive,
		Schedulable: true,
		Concurrency: 1,
	}
	accountRepo := &openAIAccountRepoStub{base: account}

	cfg := &config.Config{}
	cfg.Gateway.Scheduling.WaitTimeoutFailoverEnabled = true
	cfg.Gateway.Scheduling.WaitTimeoutFailoverAfter = 20 * time.Millisecond
	cfg.Gateway.Scheduling.WaitTimeoutFailoverMaxSwitches = 1

	gatewayService := service.NewOpenAIGatewayService(
		accountRepo,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		cfg,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)

	h := &OpenAIGatewayHandler{
		gatewayService:    gatewayService,
		concurrencyHelper: helper,
		cfg:               cfg,
	}

	c, _ := newHelperTestContext(http.MethodPost, "/openai/v1/responses")
	streamStarted := false

	selection := &service.AccountSelectionResult{
		Account: &account,
		WaitPlan: &service.AccountWaitPlan{
			AccountID:      account.ID,
			MaxConcurrency: 1,
			Timeout:        2 * time.Second,
			MaxWaiting:     100,
		},
	}

	release, acquired, rescheduleReason := h.acquireResponsesAccountSlot(
		c,
		nil,
		"session-hash",
		selection,
		"gpt-5.1",
		"load_balance",
		0,
		false,
		&streamStarted,
		zap.NewNop(),
	)
	if release != nil {
		release()
	}
	if acquired {
		t.Fatalf("expected not acquired")
	}
	if rescheduleReason != "wait_timeout_failover" {
		t.Fatalf("expected wait_timeout_failover, got %q", rescheduleReason)
	}
}

func TestOpenAIGatewayHandler_AcquireResponsesAccountSlot_NoWaitTimeoutFailoverForPreviousResponseID(t *testing.T) {
	cache := &helperConcurrencyCacheStub{}
	concurrency := service.NewConcurrencyService(cache)
	helper := NewConcurrencyHelper(concurrency, SSEPingFormatNone, 0)

	account := service.Account{
		ID:          102,
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeOAuth,
		Status:      service.StatusActive,
		Schedulable: true,
		Concurrency: 1,
	}
	accountRepo := &openAIAccountRepoStub{base: account}

	cfg := &config.Config{}
	cfg.Gateway.Scheduling.WaitTimeoutFailoverEnabled = true
	cfg.Gateway.Scheduling.WaitTimeoutFailoverAfter = 10 * time.Millisecond
	cfg.Gateway.Scheduling.WaitTimeoutFailoverMaxSwitches = 1

	gatewayService := service.NewOpenAIGatewayService(
		accountRepo,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		cfg,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)

	h := &OpenAIGatewayHandler{
		gatewayService:    gatewayService,
		concurrencyHelper: helper,
		cfg:               cfg,
	}

	c, rec := newHelperTestContext(http.MethodPost, "/openai/v1/responses")
	streamStarted := false

	selection := &service.AccountSelectionResult{
		Account: &account,
		WaitPlan: &service.AccountWaitPlan{
			AccountID:      account.ID,
			MaxConcurrency: 1,
			Timeout:        130 * time.Millisecond,
			MaxWaiting:     100,
		},
	}

	release, acquired, rescheduleReason := h.acquireResponsesAccountSlot(
		c,
		nil,
		"session-hash",
		selection,
		"gpt-5.1",
		"previous_response_id",
		0,
		false,
		&streamStarted,
		zap.NewNop(),
	)
	if release != nil {
		release()
	}
	if acquired {
		t.Fatalf("expected not acquired")
	}
	if rescheduleReason != "" {
		t.Fatalf("expected no reschedule reason, got %q", rescheduleReason)
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}
}

func TestOpenAIGatewayHandler_AcquireResponsesAccountSlot_AbortWhenAccountBecomesUnschedulable(t *testing.T) {
	cache := &helperConcurrencyCacheStub{}
	concurrency := service.NewConcurrencyService(cache)
	helper := NewConcurrencyHelper(concurrency, SSEPingFormatNone, 0)

	account := service.Account{
		ID:          103,
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeOAuth,
		Status:      service.StatusActive,
		Schedulable: true,
		Concurrency: 1,
	}
	schedulable := atomic.Bool{}
	schedulable.Store(true)
	accountRepo := &openAIAccountRepoStub{
		base:        account,
		schedulable: &schedulable,
	}
	cfg := &config.Config{}

	gatewayService := service.NewOpenAIGatewayService(
		accountRepo,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		cfg,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)

	h := &OpenAIGatewayHandler{
		gatewayService:    gatewayService,
		concurrencyHelper: helper,
		cfg:               cfg,
	}

	c, _ := newHelperTestContext(http.MethodPost, "/openai/v1/responses")
	streamStarted := false

	selection := &service.AccountSelectionResult{
		Account: &account,
		WaitPlan: &service.AccountWaitPlan{
			AccountID:      account.ID,
			MaxConcurrency: 1,
			Timeout:        1100 * time.Millisecond,
			MaxWaiting:     100,
		},
	}

	// Flip account to unschedulable while queued.
	go func() {
		time.Sleep(100 * time.Millisecond)
		schedulable.Store(false)
	}()

	release, acquired, rescheduleReason := h.acquireResponsesAccountSlot(
		c,
		nil,
		"session-hash",
		selection,
		"gpt-5.1",
		"load_balance",
		0,
		false,
		&streamStarted,
		zap.NewNop(),
	)
	if release != nil {
		release()
	}
	if acquired {
		t.Fatalf("expected not acquired")
	}
	if rescheduleReason == "" || rescheduleReason == "wait_timeout_failover" {
		t.Fatalf("expected account_unschedulable abort, got %q", rescheduleReason)
	}
}
