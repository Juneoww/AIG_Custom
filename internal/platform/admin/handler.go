package admin

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Handler struct {
	users  *identity.Service
	audits *audit.Service
}

func NewHandler(users *identity.Service, audits *audit.Service) *Handler {
	return &Handler{users: users, audits: audits}
}

func (handler *Handler) Register(group *gin.RouterGroup) {
	group.GET("/users", handler.listUsers)
	group.POST("/users", handler.createUser)
	group.PUT("/users/:userID/role", handler.assignRole)
	group.PUT("/users/:userID/active", handler.setActive)
	group.POST("/users/:userID/password-reset", handler.resetPassword)
	group.GET("/audit-events", handler.listAuditEvents)
	group.POST("/audit-events/prepared/:requestID/finalize", handler.finalizePreparedAuditEvent)
	group.POST("/audit-events/reconcile", handler.reconcileAuditEvents)
}

func (handler *Handler) listUsers(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok {
		return
	}
	users, err := handler.users.ListUsers(c.Request.Context())
	if err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	response := make([]UserResponse, 0, len(users))
	for index := range users {
		response = append(response, userResponse(&users[index]))
	}
	c.JSON(http.StatusOK, response)
}

func (handler *Handler) createUser(c *gin.Context) {
	actor, ok := requireAdmin(c)
	if !ok {
		return
	}
	var request CreateUserRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.Status(http.StatusBadRequest)
		return
	}
	targetUserID := uuid.NewString()
	metadata := map[string]any{"username": request.Username, "role": request.Role}
	mutation, err := audit.BeginMutation(c.Request.Context(), handler.audits, actor, audit.EventInput{
		Action: audit.ActionAccountCreated, ResourceType: "user", ResourceID: targetUserID, Metadata: metadata,
	})
	if err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	var user *identity.User
	var businessErr error
	err = mutation.Run(c.Request.Context(), targetUserID, metadata, func(transactionContext context.Context) error {
		user, businessErr = handler.users.CreateUser(transactionContext, identity.CreateUserInput{
			ID: targetUserID, Username: request.Username, Password: request.Password, Role: request.Role, MustChangePassword: true,
		})
		return businessErr
	})
	if businessErr != nil {
		c.Status(http.StatusBadRequest)
		return
	}
	if err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	c.JSON(http.StatusCreated, userResponse(user))
}

func (handler *Handler) assignRole(c *gin.Context) {
	actor, ok := requireAdmin(c)
	if !ok {
		return
	}
	var request AssignRoleRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.Status(http.StatusBadRequest)
		return
	}
	metadata := map[string]any{"role": request.Role}
	mutation, err := audit.BeginMutation(c.Request.Context(), handler.audits, actor, audit.EventInput{
		Action: audit.ActionRoleAssigned, ResourceType: "user", ResourceID: c.Param("userID"), Metadata: metadata,
	})
	if err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	var businessErr error
	err = mutation.Run(c.Request.Context(), "", metadata, func(transactionContext context.Context) error {
		businessErr = handler.users.SetRole(transactionContext, c.Param("userID"), request.Role)
		return businessErr
	})
	if businessErr != nil {
		respondIdentityError(c, businessErr)
		return
	}
	if err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	c.Status(http.StatusNoContent)
}

func (handler *Handler) setActive(c *gin.Context) {
	actor, ok := requireAdmin(c)
	if !ok {
		return
	}
	var request SetActiveRequest
	if err := c.ShouldBindJSON(&request); err != nil || request.Active == nil {
		c.Status(http.StatusBadRequest)
		return
	}
	action := audit.ActionAccountDisabled
	if *request.Active {
		action = audit.ActionAccountEnabled
	}
	metadata := map[string]any{"active": *request.Active}
	mutation, err := audit.BeginMutation(c.Request.Context(), handler.audits, actor, audit.EventInput{
		Action: action, ResourceType: "user", ResourceID: c.Param("userID"), Metadata: metadata,
	})
	if err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	var businessErr error
	err = mutation.Run(c.Request.Context(), "", metadata, func(transactionContext context.Context) error {
		businessErr = handler.users.SetActiveByID(transactionContext, c.Param("userID"), *request.Active)
		return businessErr
	})
	if businessErr != nil {
		respondIdentityError(c, businessErr)
		return
	}
	if err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	c.Status(http.StatusNoContent)
}

func (handler *Handler) resetPassword(c *gin.Context) {
	actor, ok := requireAdmin(c)
	if !ok {
		return
	}
	mutation, err := audit.BeginMutation(c.Request.Context(), handler.audits, actor, audit.EventInput{
		Action: audit.ActionPasswordResetRequested, ResourceType: "user", ResourceID: c.Param("userID"),
	})
	if err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	var businessErr error
	err = mutation.Run(c.Request.Context(), "", nil, func(transactionContext context.Context) error {
		_, businessErr = handler.users.CreatePasswordReset(transactionContext, c.Param("userID"))
		return businessErr
	})
	if businessErr != nil {
		respondIdentityError(c, businessErr)
		return
	}
	if err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	// Reset tokens are delivered only through a configured out-of-band channel.
	c.Status(http.StatusNoContent)
}

func (handler *Handler) listAuditEvents(c *gin.Context) {
	subject, ok := identity.CurrentSubject(c)
	if !ok {
		c.Status(http.StatusUnauthorized)
		return
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	events, err := handler.audits.Query(c.Request.Context(), subject, audit.Filter{
		Action: audit.Action(c.Query("action")), ActorUserID: c.Query("actor_user_id"),
		ResourceType: c.Query("resource_type"), ResourceID: c.Query("resource_id"), Limit: limit,
	})
	if errors.Is(err, audit.ErrForbidden) {
		c.Status(http.StatusForbidden)
		return
	}
	if err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	c.JSON(http.StatusOK, events)
}

func (handler *Handler) reconcileAuditEvents(c *gin.Context) {
	subject, ok := requireAdmin(c)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	reconciled, err := handler.audits.Reconcile(c.Request.Context(), subject, limit)
	if errors.Is(err, audit.ErrForbidden) {
		c.Status(http.StatusForbidden)
		return
	}
	if err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	c.JSON(http.StatusOK, gin.H{"reconciled": reconciled})
}

func (handler *Handler) finalizePreparedAuditEvent(c *gin.Context) {
	subject, ok := requireAdmin(c)
	if !ok {
		return
	}
	var request FinalizePreparedAuditRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.Status(http.StatusBadRequest)
		return
	}
	_, err := handler.audits.FinalizePrepared(c.Request.Context(), subject, c.Param("requestID"), request.Outcome, request.Metadata)
	switch {
	case errors.Is(err, audit.ErrForbidden):
		c.Status(http.StatusForbidden)
	case errors.Is(err, audit.ErrInvalidCompletionOutcome):
		c.Status(http.StatusBadRequest)
	case errors.Is(err, audit.ErrCompletionNotFound):
		c.Status(http.StatusNotFound)
	case errors.Is(err, audit.ErrCompletionConflict):
		c.Status(http.StatusConflict)
	case err != nil:
		c.Status(http.StatusInternalServerError)
	default:
		c.Status(http.StatusNoContent)
	}
}

func requireAdmin(c *gin.Context) (identity.Subject, bool) {
	subject, ok := identity.CurrentSubject(c)
	if !ok {
		c.Status(http.StatusUnauthorized)
		return identity.Subject{}, false
	}
	if subject.Role != identity.RoleAdmin {
		c.Status(http.StatusForbidden)
		return identity.Subject{}, false
	}
	return subject, true
}

func respondIdentityError(c *gin.Context, err error) {
	if errors.Is(err, identity.ErrNotFound) {
		c.Status(http.StatusNotFound)
		return
	}
	c.Status(http.StatusBadRequest)
}
