package knowledge

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failingKnowledgeAuditRepository struct {
	delegate *audit.MemoryRepository
	failOn   int
	calls    int
}

type retryableCompletionRepository struct {
	delegate   *audit.MemoryRepository
	readyCalls int
	identities map[string]struct{}
}

func (repository *retryableCompletionRepository) Append(ctx context.Context, event *audit.Event) error {
	return repository.delegate.Append(ctx, event)
}

func (repository *retryableCompletionRepository) List(ctx context.Context, filter audit.Filter) ([]audit.Event, error) {
	return repository.delegate.List(ctx, filter)
}

func (repository *retryableCompletionRepository) EnqueueCompletion(ctx context.Context, completion *audit.CompletionOutbox) error {
	if repository.identities == nil {
		repository.identities = map[string]struct{}{}
	}
	repository.identities[completion.ID+"\x00"+completion.EventID] = struct{}{}
	if completion.Outcome == audit.OutcomeSuccess {
		repository.readyCalls++
		if repository.readyCalls == 1 {
			return errors.New("injected ready completion failure")
		}
	}
	return repository.delegate.EnqueueCompletion(ctx, completion)
}

func (repository *retryableCompletionRepository) Completion(ctx context.Context, id string) (*audit.CompletionOutbox, error) {
	return repository.delegate.Completion(ctx, id)
}

func (repository *retryableCompletionRepository) ListPendingCompletions(ctx context.Context, limit int) ([]audit.CompletionOutbox, error) {
	return repository.delegate.ListPendingCompletions(ctx, limit)
}

func (repository *retryableCompletionRepository) UpdateCompletion(ctx context.Context, completion *audit.CompletionOutbox) error {
	return repository.delegate.UpdateCompletion(ctx, completion)
}

func (repository *retryableCompletionRepository) EventExists(ctx context.Context, id string) (bool, error) {
	return repository.delegate.EventExists(ctx, id)
}

func (repository *failingKnowledgeAuditRepository) Append(ctx context.Context, event *audit.Event) error {
	repository.calls++
	if repository.calls == repository.failOn {
		return errors.New("injected audit append failure")
	}
	return repository.delegate.Append(ctx, event)
}

func (repository *failingKnowledgeAuditRepository) List(ctx context.Context, filter audit.Filter) ([]audit.Event, error) {
	return repository.delegate.List(ctx, filter)
}

func (repository *failingKnowledgeAuditRepository) EnqueueCompletion(ctx context.Context, completion *audit.CompletionOutbox) error {
	return repository.delegate.EnqueueCompletion(ctx, completion)
}

func (repository *failingKnowledgeAuditRepository) Completion(ctx context.Context, id string) (*audit.CompletionOutbox, error) {
	return repository.delegate.Completion(ctx, id)
}

func (repository *failingKnowledgeAuditRepository) ListPendingCompletions(ctx context.Context, limit int) ([]audit.CompletionOutbox, error) {
	return repository.delegate.ListPendingCompletions(ctx, limit)
}

func (repository *failingKnowledgeAuditRepository) UpdateCompletion(ctx context.Context, completion *audit.CompletionOutbox) error {
	return repository.delegate.UpdateCompletion(ctx, completion)
}

func (repository *failingKnowledgeAuditRepository) EventExists(ctx context.Context, id string) (bool, error) {
	return repository.delegate.EventExists(ctx, id)
}

func TestGovernKeepsLegacySuccessWhenCompletionAppendFailsAndReconciles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	identityService := identity.NewService(identity.NewMemoryRepository())
	_, err := identityService.CreateUser(ctx, identity.CreateUserInput{Username: "admin", Password: "secret", Role: identity.RoleAdmin})
	require.NoError(t, err)
	login, err := identityService.Authenticate(ctx, "admin", "secret")
	require.NoError(t, err)
	auditRepository := audit.NewMemoryRepository()
	auditService := audit.NewService(&failingKnowledgeAuditRepository{delegate: auditRepository, failOn: 2})
	mutated := false

	router := gin.New()
	router.POST("/knowledge", identity.Authenticate(identityService, identity.CookiePolicy{}),
		NewHandler(NewService(auditService)).Govern(KindFingerprint, OperationUpdate, func(c *gin.Context) {
			mutated = true
			c.JSON(http.StatusOK, gin.H{"message": "legacy success"})
		}))
	request := httptest.NewRequest(http.MethodPost, "/knowledge", nil)
	request.AddCookie(&http.Cookie{Name: "aig_session", Value: login.Token})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	assert.True(t, mutated)
	assert.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), "legacy success")
	events, err := auditRepository.List(ctx, audit.Filter{})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, audit.OutcomePending, events[0].Outcome)
	pending, err := auditService.PendingCompletions(ctx, identity.Subject{Role: identity.RoleAdmin}, 10)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	reconciled, err := auditService.Reconcile(ctx, identity.Subject{Role: identity.RoleAdmin}, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, reconciled)
	events, err = auditRepository.List(ctx, audit.Filter{})
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, audit.OutcomeSuccess, events[1].Outcome)
}

func TestGovernKeepsLegacySuccessWhenReadyFinalizeNeedsAdminRecovery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	identityService := identity.NewService(identity.NewMemoryRepository())
	_, err := identityService.CreateUser(ctx, identity.CreateUserInput{Username: "admin", Password: "secret", Role: identity.RoleAdmin})
	require.NoError(t, err)
	login, err := identityService.Authenticate(ctx, "admin", "secret")
	require.NoError(t, err)
	auditRepository := audit.NewMemoryRepository()
	repository := &retryableCompletionRepository{delegate: auditRepository}
	auditService := audit.NewService(repository)
	mutations := 0

	router := gin.New()
	router.POST("/knowledge", identity.Authenticate(identityService, identity.CookiePolicy{}),
		NewHandler(NewService(auditService)).Govern(KindFingerprint, OperationUpdate, func(c *gin.Context) {
			mutations++
			c.JSON(http.StatusOK, gin.H{"message": "legacy success"})
		}))
	request := httptest.NewRequest(http.MethodPost, "/knowledge", nil)
	request.AddCookie(&http.Cookie{Name: "aig_session", Value: login.Token})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	assert.Equal(t, 1, mutations)
	assert.Equal(t, http.StatusOK, response.Code, "a completed file mutation keeps the legacy response while an administrator recovers its prepared audit intent")
	assert.Contains(t, response.Body.String(), "legacy success")
	events, err := auditRepository.List(ctx, audit.Filter{})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, audit.OutcomePending, events[0].Outcome)
	assert.NotEmpty(t, events[0].RequestID, "the administrator can locate the recovery request through the audit list")
	assert.Equal(t, events[0].RequestID, response.Header().Get(AuditRequestIDHeader))
	assert.NotContains(t, response.Body.String(), "injected ready completion failure")
	pending, err := auditService.PendingCompletions(ctx, identity.Subject{Role: identity.RoleAdmin}, 10)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, audit.CompletionStatePrepared, pending[0].State)
	reconciled, err := auditService.Reconcile(ctx, identity.Subject{Role: identity.RoleAdmin}, 10)
	require.NoError(t, err)
	assert.Zero(t, reconciled, "generic reconciliation must not guess the outcome of a prepared file mutation")
}

func TestGovernAsyncRecordsCompletionOnlyAfterBackgroundResult(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	identityService := identity.NewService(identity.NewMemoryRepository())
	_, err := identityService.CreateUser(ctx, identity.CreateUserInput{Username: "admin", Password: "secret", Role: identity.RoleAdmin})
	require.NoError(t, err)
	login, err := identityService.Authenticate(ctx, "admin", "secret")
	require.NoError(t, err)
	auditRepository := audit.NewMemoryRepository()
	auditService := audit.NewService(auditRepository)
	var completion AsyncCompletion

	router := gin.New()
	router.POST("/update", identity.Authenticate(identityService, identity.CookiePolicy{}),
		NewHandler(NewService(auditService)).GovernAsync(KindSystemData, OperationUpdate, func(c *gin.Context) {
			var ok bool
			completion, ok = CurrentAsyncCompletion(c)
			require.True(t, ok)
			c.Status(http.StatusAccepted)
		}))
	request := httptest.NewRequest(http.MethodPost, "/update", nil)
	request.AddCookie(&http.Cookie{Name: "aig_session", Value: login.Token})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	assert.Equal(t, http.StatusAccepted, response.Code)

	events, err := auditRepository.List(ctx, audit.Filter{})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, audit.ActionKnowledgeChangeRequested, events[0].Action)
	assert.Equal(t, audit.OutcomePending, events[0].Outcome)

	require.NoError(t, completion(ctx, true, map[string]any{"files_updated": 3}))
	events, err = auditRepository.List(ctx, audit.Filter{})
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, audit.ActionKnowledgeChanged, events[1].Action)
	assert.Equal(t, audit.OutcomeSuccess, events[1].Outcome)
	assert.Equal(t, events[0].RequestID, events[1].RequestID)
}

func TestGovernAsyncCompletionCanRetryAfterDurableFinalizeFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	identityService := identity.NewService(identity.NewMemoryRepository())
	_, err := identityService.CreateUser(ctx, identity.CreateUserInput{Username: "admin", Password: "secret", Role: identity.RoleAdmin})
	require.NoError(t, err)
	login, err := identityService.Authenticate(ctx, "admin", "secret")
	require.NoError(t, err)
	auditRepository := audit.NewMemoryRepository()
	repository := &retryableCompletionRepository{delegate: auditRepository}
	auditService := audit.NewService(repository)
	var completion AsyncCompletion

	router := gin.New()
	router.POST("/update", identity.Authenticate(identityService, identity.CookiePolicy{}),
		NewHandler(NewService(auditService)).GovernAsync(KindSystemData, OperationUpdate, func(c *gin.Context) {
			completion, _ = CurrentAsyncCompletion(c)
			c.Status(http.StatusAccepted)
		}))
	request := httptest.NewRequest(http.MethodPost, "/update", nil)
	request.AddCookie(&http.Cookie{Name: "aig_session", Value: login.Token})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusAccepted, response.Code)

	require.Error(t, completion(ctx, true, map[string]any{"files_updated": 3}))
	require.NoError(t, completion(ctx, true, map[string]any{"files_updated": 3}))
	require.NoError(t, completion(ctx, true, map[string]any{"files_updated": 3}))
	assert.Equal(t, 2, repository.readyCalls, "the callback locks only after a durable finalize succeeds")
	require.Len(t, repository.identities, 1, "prepared and retried completion must reuse one outbox ID")
	events, err := auditRepository.List(ctx, audit.Filter{})
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, audit.OutcomePending, events[0].Outcome)
	assert.Equal(t, audit.OutcomeSuccess, events[1].Outcome)
}
