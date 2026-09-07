package idempotency

import (
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestMCPMutationReplayResponseStaysStrict(t *testing.T) {
	response := SafeResponse{ID: uuid.NewString(), CurrentVersion: 2, ResourceRevision: "4", Status: "updated"}
	encoded, err := encodeSafeResponse(response)
	require.NoError(t, err)
	decoded, err := decodeSafeResponse(encoded)
	require.NoError(t, err)
	require.Equal(t, response, decoded)
	_, err = decodeSafeResponse([]byte(`{"id":"` + response.ID + `","current_version":2,"resource_revision":"4","status":"updated","secret":"private"}`))
	require.Error(t, err)
	_, err = encodeSafeResponse(SafeResponse{TaskID: uuid.NewString(), ID: uuid.NewString(), Status: "pending"})
	require.Error(t, err)
}

func TestMCPStrictWireRejectsNullAndCaseAliases(t *testing.T) {
	var empty struct{}
	require.Error(t, DecodeStrict([]byte(`null`), &empty))
	var value struct {
		SourceKind string `json:"source_kind"`
	}
	require.Error(t, DecodeStrict([]byte(`{"Source_Kind":"repository"}`), &value))
	require.Error(t, DecodeStrict([]byte(`{"source_kind":"repository","Source_Kind":"service"}`), &value))
	require.NoError(t, DecodeStrict([]byte(`{"source_kind":"repository"}`), &value))
}
