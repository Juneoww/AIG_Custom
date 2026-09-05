package idempotency

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/google/uuid"
)

const (
	retentionPeriod       = 24 * time.Hour
	maxIdempotencyKeySize = 128
	maxPayloadBytes       = 64 << 10
)

var (
	ErrInvalidScope        = errors.New("MCP 幂等范围无效")
	ErrKeyReused           = errors.New("MCP 幂等键已用于不同请求")
	ErrUnsafeResponse      = errors.New("MCP 幂等响应无效")
	ErrSuccessNotPersisted = errors.New("MCP 幂等成功结果未持久化")
)

// Service owns trusted scope derivation and replay decisions. It has no HTTP
// handler dependency, so query/header/tenant input cannot enter scope logic.
type Service struct {
	repository Repository
	now        func() time.Time
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository, now: func() time.Time { return time.Now().UTC() }}
}

// Claim is issued only to the one callback that won a fresh idempotency key.
// PersistSuccess must be the final write inside the caller's audit transaction.
type Claim struct {
	service   *Service
	identity  recordIdentity
	hash      []byte
	persisted bool
	result    Result
}

// Execute serializes an MCP request key, safely returns an unexpired prior
// success, or gives the winner a Claim. The callback is responsible for
// placing Claim.PersistSuccess inside its governing business transaction.
func (service *Service) Execute(ctx context.Context, subject identity.Subject, operation Operation, apply func(context.Context, *Claim) error) (Result, error) {
	if service == nil || service.repository == nil || apply == nil {
		return Result{}, ErrInvalid
	}
	identity, payloadHash, err := normalizedIdentity(subject, operation)
	if err != nil {
		return Result{}, err
	}
	now := service.now().UTC()
	var outcome Result
	err = service.repository.WithinKeyLock(ctx, identity.advisoryLockKey(), func(lockedContext context.Context) error {
		record, getErr := service.repository.Get(lockedContext, identity)
		if getErr == nil {
			if !record.ExpiresAt.After(now) {
				if err := service.repository.Delete(lockedContext, identity); err != nil && !errors.Is(err, ErrNotFound) {
					return err
				}
			} else {
				if !bytes.Equal(record.PayloadHash, payloadHash) {
					return ErrKeyReused
				}
				response, err := decodeSafeResponse(record.SafeResponse)
				if err != nil {
					return ErrUnsafeResponse
				}
				outcome = Result{StatusCode: record.StatusCode, Response: response, Replay: true}
				return nil
			}
		} else if !errors.Is(getErr, ErrNotFound) {
			return getErr
		}

		claim := &Claim{service: service, identity: identity, hash: append([]byte(nil), payloadHash...)}
		if err := apply(lockedContext, claim); err != nil {
			return err
		}
		if !claim.persisted {
			return ErrSuccessNotPersisted
		}
		outcome = claim.result
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	return outcome, nil
}

// PersistSuccess writes the whitelisted replay response using the transaction
// already carried by ctx. It never opens a nested transaction.
func (claim *Claim) PersistSuccess(ctx context.Context, statusCode int, response SafeResponse) error {
	if claim == nil || claim.service == nil || claim.service.repository == nil || claim.persisted || statusCode < 200 || statusCode >= 300 {
		return ErrUnsafeResponse
	}
	encoded, err := encodeSafeResponse(response)
	if err != nil {
		return ErrUnsafeResponse
	}
	now := claim.service.now().UTC()
	record := &Record{
		ID: uuid.NewString(), PrincipalID: claim.identity.principalID, ScopeKey: claim.identity.scopeKey,
		Method: claim.identity.method, Path: claim.identity.path, IdempotencyKey: claim.identity.key,
		PayloadHash: append([]byte(nil), claim.hash...), StatusCode: statusCode, SafeResponse: encoded,
		CreatedAt: now, ExpiresAt: now.Add(retentionPeriod),
	}
	if err := claim.service.repository.Create(ctx, record); err != nil {
		return err
	}
	claim.persisted = true
	claim.result = Result{StatusCode: statusCode, Response: response}
	return nil
}

// CleanupExpired removes records only once their full retention period ends.
func (service *Service) CleanupExpired(ctx context.Context) (int, error) {
	if service == nil || service.repository == nil {
		return 0, ErrInvalid
	}
	return service.repository.CleanupExpired(ctx, service.now().UTC())
}

// DeriveScopeKey is the sole scope construction boundary. No HTTP header,
// tenant query parameter, or untrusted string can choose a stored scope key.
func DeriveScopeKey(subject identity.Subject, scope Scope) (string, error) {
	if strings.TrimSpace(subject.UserID) == "" {
		return "", ErrInvalidScope
	}
	switch scope {
	case ScopePrivate:
		if subject.Role != identity.RoleUser && subject.Role != identity.RoleAdmin {
			return "", ErrInvalidScope
		}
		return "private:" + subject.UserID, nil
	case ScopeGlobal:
		if subject.Role != identity.RoleAdmin {
			return "", ErrInvalidScope
		}
		return "global", nil
	default:
		return "", ErrInvalidScope
	}
}

func normalizedIdentity(subject identity.Subject, operation Operation) (recordIdentity, []byte, error) {
	scopeKey, err := DeriveScopeKey(subject, operation.Scope)
	if err != nil {
		return recordIdentity{}, nil, err
	}
	method := strings.ToUpper(strings.TrimSpace(operation.Method))
	if method != "POST" && method != "PUT" && method != "PATCH" && method != "DELETE" {
		return recordIdentity{}, nil, ErrInvalid
	}
	normalizedPath, err := normalizePath(operation.Path)
	if err != nil {
		return recordIdentity{}, nil, ErrInvalid
	}
	key := strings.TrimSpace(operation.Key)
	if !validIdempotencyKey(key) {
		return recordIdentity{}, nil, ErrInvalid
	}
	canonical, err := canonicalPayload(operation.Payload)
	if err != nil {
		return recordIdentity{}, nil, ErrInvalid
	}
	digest := sha256.Sum256(canonical)
	return recordIdentity{principalID: subject.UserID, scopeKey: scopeKey, method: method, path: normalizedPath, key: key}, digest[:], nil
}

func validIdempotencyKey(value string) bool {
	if value == "" || len(value) > maxIdempotencyKeySize {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '-' || character == '_' || character == '.' {
			continue
		}
		return false
	}
	return true
}

func normalizePath(raw string) (string, error) {
	parsed, err := url.ParseRequestURI(strings.TrimSpace(raw))
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.Fragment != "" || !strings.HasPrefix(parsed.Path, "/") {
		return "", ErrInvalid
	}
	cleaned := path.Clean(parsed.Path)
	if cleaned == "." || !strings.HasPrefix(cleaned, "/") {
		return "", ErrInvalid
	}
	return cleaned, nil
}

func canonicalPayload(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 || len(raw) > maxPayloadBytes {
		return nil, ErrInvalid
	}
	if err := validateUniqueJSONMembers(raw); err != nil {
		return nil, ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, ErrInvalid
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, ErrInvalid
	}
	canonicalValue, err := canonicalizeJSONValue(value)
	if err != nil {
		return nil, ErrInvalid
	}
	canonical, err := json.Marshal(canonicalValue)
	if err != nil {
		return nil, ErrInvalid
	}
	return canonical, nil
}

// validateUniqueJSONMembers rejects ambiguous object members before the
// canonical decoder turns them into a map. This keeps the digest aligned with
// strict DTO parsing instead of silently choosing one duplicate value.
func validateUniqueJSONMembers(raw json.RawMessage) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := validateJSONValue(decoder); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return ErrInvalid
	}
	return nil
}

func validateJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return nil
	}
	switch delimiter {
	case '{':
		members := map[string]struct{}{}
		for decoder.More() {
			memberToken, err := decoder.Token()
			member, isMember := memberToken.(string)
			if err != nil || !isMember {
				return ErrInvalid
			}
			if _, duplicate := members[member]; duplicate {
				return ErrInvalid
			}
			members[member] = struct{}{}
			if err := validateJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return ErrInvalid
		}
		return nil
	case '[':
		for decoder.More() {
			if err := validateJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return ErrInvalid
		}
		return nil
	default:
		return ErrInvalid
	}
}

// canonicalJSONNumber always marshals a JSON number in one exact scientific
// representation, so equivalent number spellings produce the same payload
// digest without converting through an imprecise float64.
type canonicalJSONNumber string

func (number canonicalJSONNumber) MarshalJSON() ([]byte, error) {
	return []byte(number), nil
}

func canonicalizeJSONValue(value any) (any, error) {
	switch typed := value.(type) {
	case nil, bool, string:
		return typed, nil
	case json.Number:
		return canonicalizeJSONNumber(typed)
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			canonical, err := canonicalizeJSONValue(item)
			if err != nil {
				return nil, err
			}
			result[index] = canonical
		}
		return result, nil
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			canonical, err := canonicalizeJSONValue(item)
			if err != nil {
				return nil, err
			}
			result[key] = canonical
		}
		return result, nil
	default:
		return nil, ErrInvalid
	}
}

func canonicalizeJSONNumber(number json.Number) (canonicalJSONNumber, error) {
	literal := number.String()
	negative := strings.HasPrefix(literal, "-")
	if negative {
		literal = literal[1:]
	}
	exponentLiteral := "0"
	if index := strings.IndexAny(literal, "eE"); index >= 0 {
		exponentLiteral = literal[index+1:]
		literal = literal[:index]
	}
	integer, fraction, hasFraction := strings.Cut(literal, ".")
	if integer == "" || (hasFraction && fraction == "") || !decimalDigits(integer) || (hasFraction && !decimalDigits(fraction)) {
		return "", ErrInvalid
	}
	exponent, validExponent := new(big.Int).SetString(exponentLiteral, 10)
	if !validExponent {
		return "", ErrInvalid
	}
	significant := strings.TrimLeft(integer+fraction, "0")
	if significant == "" {
		return canonicalJSONNumber("0"), nil
	}
	trailingZeroes := len(significant) - len(strings.TrimRight(significant, "0"))
	significant = strings.TrimRight(significant, "0")
	scale := new(big.Int).Sub(exponent, big.NewInt(int64(len(fraction))))
	scale.Add(scale, big.NewInt(int64(trailingZeroes)))
	scientificExponent := scale.Add(scale, big.NewInt(int64(len(significant)-1)))
	canonical := string(significant[0])
	if len(significant) > 1 {
		canonical += "." + significant[1:]
	}
	if negative {
		canonical = "-" + canonical
	}
	return canonicalJSONNumber(canonical + "e" + scientificExponent.String()), nil
}

func decimalDigits(value string) bool {
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func encodeSafeResponse(response SafeResponse) (json.RawMessage, error) {
	if err := validateSafeResponse(response); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func decodeSafeResponse(raw json.RawMessage) (SafeResponse, error) {
	if len(raw) == 0 {
		return SafeResponse{}, ErrUnsafeResponse
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return SafeResponse{}, ErrUnsafeResponse
	}
	fields := make(map[string]json.RawMessage, 2)
	for decoder.More() {
		fieldToken, err := decoder.Token()
		fieldName, isFieldName := fieldToken.(string)
		if err != nil || !isFieldName {
			return SafeResponse{}, ErrUnsafeResponse
		}
		if _, duplicate := fields[fieldName]; duplicate {
			return SafeResponse{}, ErrUnsafeResponse
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return SafeResponse{}, ErrUnsafeResponse
		}
		fields[fieldName] = value
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return SafeResponse{}, ErrUnsafeResponse
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF || len(fields) != 2 {
		return SafeResponse{}, ErrUnsafeResponse
	}
	taskID, hasTaskID := fields["task_id"]
	status, hasStatus := fields["status"]
	if !hasTaskID || !hasStatus {
		return SafeResponse{}, ErrUnsafeResponse
	}
	var response SafeResponse
	if json.Unmarshal(taskID, &response.TaskID) != nil || json.Unmarshal(status, &response.Status) != nil || validateSafeResponse(response) != nil {
		return SafeResponse{}, ErrUnsafeResponse
	}
	return response, nil
}

func validateSafeResponse(response SafeResponse) error {
	if _, err := uuid.Parse(strings.TrimSpace(response.TaskID)); err != nil {
		return ErrUnsafeResponse
	}
	switch response.Status {
	case "pending", "dispatching", "running", "dispatch_failed", "dispatch_unknown":
		return nil
	default:
		return ErrUnsafeResponse
	}
}
