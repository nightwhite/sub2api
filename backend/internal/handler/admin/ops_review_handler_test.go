package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type opsSystemLogRepoStub struct {
	service.OpsRepository

	listResp *service.OpsSystemLogList
	listErr  error

	cleanupDeleted int64
	cleanupErr     error

	lastListFilter    *service.OpsSystemLogFilter
	lastCleanupFilter *service.OpsSystemLogCleanupFilter
	cleanupAuditCount int
}

func (s *opsSystemLogRepoStub) ListSystemLogs(ctx context.Context, filter *service.OpsSystemLogFilter) (*service.OpsSystemLogList, error) {
	if filter != nil {
		cloned := *filter
		s.lastListFilter = &cloned
	}
	if s.listResp != nil {
		return s.listResp, s.listErr
	}
	page := 1
	pageSize := 50
	if filter != nil {
		if filter.Page > 0 {
			page = filter.Page
		}
		if filter.PageSize > 0 {
			pageSize = filter.PageSize
		}
	}
	return &service.OpsSystemLogList{
		Logs:     []*service.OpsSystemLog{},
		Total:    0,
		Page:     page,
		PageSize: pageSize,
	}, s.listErr
}

func (s *opsSystemLogRepoStub) DeleteSystemLogs(ctx context.Context, filter *service.OpsSystemLogCleanupFilter) (int64, error) {
	if filter != nil {
		cloned := *filter
		s.lastCleanupFilter = &cloned
	}
	return s.cleanupDeleted, s.cleanupErr
}

func (s *opsSystemLogRepoStub) InsertSystemLogCleanupAudit(ctx context.Context, input *service.OpsSystemLogCleanupAudit) error {
	s.cleanupAuditCount++
	return nil
}

type runtimeSettingRepoStub struct {
	service.SettingRepository

	values    map[string]string
	setErr    error
	deleteErr error
}

func (s *runtimeSettingRepoStub) GetValue(ctx context.Context, key string) (string, error) {
	if s.values == nil {
		return "", service.ErrSettingNotFound
	}
	value, ok := s.values[key]
	if !ok {
		return "", service.ErrSettingNotFound
	}
	return value, nil
}

func (s *runtimeSettingRepoStub) Set(ctx context.Context, key, value string) error {
	if s.setErr != nil {
		return s.setErr
	}
	if s.values == nil {
		s.values = make(map[string]string)
	}
	s.values[key] = value
	return nil
}

func (s *runtimeSettingRepoStub) Delete(ctx context.Context, key string) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	if s.values == nil {
		return service.ErrSettingNotFound
	}
	if _, ok := s.values[key]; !ok {
		return service.ErrSettingNotFound
	}
	delete(s.values, key)
	return nil
}

func newOpsServiceForReviewHandlerTests(opsRepo service.OpsRepository, settingRepo service.SettingRepository) *service.OpsService {
	cfg := &config.Config{}
	cfg.Ops.Enabled = true
	cfg.Log.Level = "info"
	cfg.Log.Caller = true
	cfg.Log.StacktraceLevel = "error"
	cfg.Log.Sampling.Enabled = false
	cfg.Log.Sampling.Initial = 100
	cfg.Log.Sampling.Thereafter = 100
	cfg.Log.Rotation.MaxAgeDays = 30

	return service.NewOpsService(
		opsRepo,
		settingRepo,
		cfg,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
}

func setupOpsSystemLogTestRouter(opsService *service.OpsService, withAuth bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	if withAuth {
		router.Use(func(c *gin.Context) {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 99})
			c.Next()
		})
	}

	h := NewOpsHandler(opsService)
	router.GET("/api/v1/admin/ops/system-logs", h.ListSystemLogs)
	router.POST("/api/v1/admin/ops/system-logs/cleanup", h.CleanupSystemLogs)
	return router
}

func setupOpsRuntimeLoggingTestRouter(opsService *service.OpsService, withAuth bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	if withAuth {
		router.Use(func(c *gin.Context) {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 99})
			c.Next()
		})
	}

	h := NewOpsHandler(opsService)
	router.GET("/api/v1/admin/ops/runtime/logging", h.GetRuntimeLogConfig)
	router.PUT("/api/v1/admin/ops/runtime/logging", h.UpdateRuntimeLogConfig)
	router.POST("/api/v1/admin/ops/runtime/logging/reset", h.ResetRuntimeLogConfig)
	return router
}

func decodeAPIResponse(t *testing.T, recorder *httptest.ResponseRecorder) response.Response {
	t.Helper()
	var resp response.Response
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	return resp
}

func decodeRuntimeConfigData(t *testing.T, data any) service.OpsRuntimeLogConfig {
	t.Helper()
	raw, err := json.Marshal(data)
	require.NoError(t, err)
	var cfg service.OpsRuntimeLogConfig
	require.NoError(t, json.Unmarshal(raw, &cfg))
	return cfg
}

func TestOpsSystemLogHandlerListPaginationAndTimeRange(t *testing.T) {
	repo := &opsSystemLogRepoStub{}
	opsService := newOpsServiceForReviewHandlerTests(repo, nil)
	router := setupOpsSystemLogTestRouter(opsService, false)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/ops/system-logs?page=2&page_size=999&time_range=5m&level=error", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusOK, recorder.Code)
	resp := decodeAPIResponse(t, recorder)
	require.Equal(t, 0, resp.Code)

	require.NotNil(t, repo.lastListFilter)
	require.Equal(t, 2, repo.lastListFilter.Page)
	require.Equal(t, 200, repo.lastListFilter.PageSize)
	require.Equal(t, "error", repo.lastListFilter.Level)
	require.NotNil(t, repo.lastListFilter.StartTime)
	require.NotNil(t, repo.lastListFilter.EndTime)

	window := repo.lastListFilter.EndTime.Sub(*repo.lastListFilter.StartTime)
	require.GreaterOrEqual(t, window, 4*time.Minute)
	require.LessOrEqual(t, window, 6*time.Minute)
}

func TestOpsSystemLogHandlerListInvalidUserID(t *testing.T) {
	repo := &opsSystemLogRepoStub{}
	opsService := newOpsServiceForReviewHandlerTests(repo, nil)
	router := setupOpsSystemLogTestRouter(opsService, false)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/ops/system-logs?user_id=bad", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestOpsSystemLogHandlerCleanupUnauthorized(t *testing.T) {
	repo := &opsSystemLogRepoStub{}
	opsService := newOpsServiceForReviewHandlerTests(repo, nil)
	router := setupOpsSystemLogTestRouter(opsService, false)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/ops/system-logs/cleanup", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestOpsSystemLogHandlerCleanupRequiresAtLeastOneConstraint(t *testing.T) {
	repo := &opsSystemLogRepoStub{cleanupErr: service.ErrOpsSystemLogCleanupFilterRequired}
	opsService := newOpsServiceForReviewHandlerTests(repo, nil)
	router := setupOpsSystemLogTestRouter(opsService, true)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/ops/system-logs/cleanup", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, strings.ToLower(recorder.Body.String()), "cleanup requires at least one filter condition")
	require.NotNil(t, repo.lastCleanupFilter)
	require.Equal(t, 0, repo.cleanupAuditCount)
}

func TestOpsSystemLogHandlerCleanupInvalidStartTime(t *testing.T) {
	repo := &opsSystemLogRepoStub{}
	opsService := newOpsServiceForReviewHandlerTests(repo, nil)
	router := setupOpsSystemLogTestRouter(opsService, true)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/ops/system-logs/cleanup", bytes.NewBufferString(`{"start_time":"bad"}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestOpsRuntimeLoggingHandlerGetConfigSuccess(t *testing.T) {
	opsService := newOpsServiceForReviewHandlerTests(nil, nil)
	router := setupOpsRuntimeLoggingTestRouter(opsService, false)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/ops/runtime/logging", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusOK, recorder.Code)
	resp := decodeAPIResponse(t, recorder)
	require.Equal(t, 0, resp.Code)

	cfg := decodeRuntimeConfigData(t, resp.Data)
	require.Equal(t, "info", cfg.Level)
	require.Equal(t, "error", cfg.StacktraceLevel)
}

func TestOpsRuntimeLoggingHandlerUpdateInvalidBody(t *testing.T) {
	opsService := newOpsServiceForReviewHandlerTests(nil, nil)
	router := setupOpsRuntimeLoggingTestRouter(opsService, true)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/ops/runtime/logging", bytes.NewBufferString("{bad"))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestOpsRuntimeLoggingHandlerUpdateUnauthorized(t *testing.T) {
	settingRepo := &runtimeSettingRepoStub{values: map[string]string{}}
	opsService := newOpsServiceForReviewHandlerTests(nil, settingRepo)
	router := setupOpsRuntimeLoggingTestRouter(opsService, false)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/ops/runtime/logging", bytes.NewBufferString(`{"level":"debug","sampling_initial":100,"sampling_thereafter":100,"stacktrace_level":"error","retention_days":30}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestOpsRuntimeLoggingHandlerUpdateServiceErrorShape(t *testing.T) {
	opsService := newOpsServiceForReviewHandlerTests(nil, nil)
	router := setupOpsRuntimeLoggingTestRouter(opsService, true)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/ops/runtime/logging", bytes.NewBufferString(`{"level":"debug","enable_sampling":true,"sampling_initial":120,"sampling_thereafter":150,"caller":true,"stacktrace_level":"error","retention_days":30}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	resp := decodeAPIResponse(t, recorder)
	require.Equal(t, http.StatusServiceUnavailable, resp.Code)
	require.NotEmpty(t, resp.Message)
}

func TestOpsRuntimeLoggingHandlerUpdateSuccess(t *testing.T) {
	settingRepo := &runtimeSettingRepoStub{values: map[string]string{}}
	opsService := newOpsServiceForReviewHandlerTests(nil, settingRepo)
	router := setupOpsRuntimeLoggingTestRouter(opsService, true)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/ops/runtime/logging", bytes.NewBufferString(`{"level":"debug","enable_sampling":true,"sampling_initial":120,"sampling_thereafter":150,"caller":false,"stacktrace_level":"fatal","retention_days":45}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusOK, recorder.Code)
	resp := decodeAPIResponse(t, recorder)
	require.Equal(t, 0, resp.Code)

	cfg := decodeRuntimeConfigData(t, resp.Data)
	require.Equal(t, "debug", cfg.Level)
	require.Equal(t, "fatal", cfg.StacktraceLevel)
	require.Equal(t, int64(99), cfg.UpdatedByUserID)
	require.NotEmpty(t, settingRepo.values[service.SettingKeyOpsRuntimeLogConfig])
}

func TestOpsRuntimeLoggingHandlerResetUnauthorized(t *testing.T) {
	settingRepo := &runtimeSettingRepoStub{values: map[string]string{}}
	opsService := newOpsServiceForReviewHandlerTests(nil, settingRepo)
	router := setupOpsRuntimeLoggingTestRouter(opsService, false)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/ops/runtime/logging/reset", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestOpsRuntimeLoggingHandlerResetSuccess(t *testing.T) {
	initial := service.OpsRuntimeLogConfig{
		Level:           "debug",
		EnableSampling:  true,
		SamplingInitial: 120,
		SamplingNext:    150,
		Caller:          false,
		StacktraceLevel: "fatal",
		RetentionDays:   45,
		Source:          "runtime_setting",
	}
	raw, err := json.Marshal(initial)
	require.NoError(t, err)

	settingRepo := &runtimeSettingRepoStub{
		values: map[string]string{
			service.SettingKeyOpsRuntimeLogConfig: string(raw),
		},
	}
	opsService := newOpsServiceForReviewHandlerTests(nil, settingRepo)
	router := setupOpsRuntimeLoggingTestRouter(opsService, true)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/ops/runtime/logging/reset", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusOK, recorder.Code)
	resp := decodeAPIResponse(t, recorder)
	require.Equal(t, 0, resp.Code)

	cfg := decodeRuntimeConfigData(t, resp.Data)
	require.Equal(t, "baseline", cfg.Source)
	require.Equal(t, int64(99), cfg.UpdatedByUserID)

	_, exists := settingRepo.values[service.SettingKeyOpsRuntimeLogConfig]
	require.False(t, exists)
}

func TestRuntimeSettingRepoDeleteMissingReturnsNotFound(t *testing.T) {
	settingRepo := &runtimeSettingRepoStub{values: map[string]string{}}
	err := settingRepo.Delete(context.Background(), service.SettingKeyOpsRuntimeLogConfig)
	require.True(t, errors.Is(err, service.ErrSettingNotFound))
}
