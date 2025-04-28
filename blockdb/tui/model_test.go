package tui

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/chatton/interchaintest/v1/blockdb"
)

func TestModel_RootView(t *testing.T) {
	m := NewModel(&mockQueryService{}, "test.db", "abc123", time.Now(), make([]blockdb.TestCaseResult, 1))
	view := m.RootView()
	require.NotNil(t, view)
	require.Positive(t, view.GetItemCount())
}
