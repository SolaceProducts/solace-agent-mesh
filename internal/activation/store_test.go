// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package activation

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoreRoundTripsARecord(t *testing.T) {
	store := NewStore(t.TempDir())
	signedIn := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	record := Record{
		Account: &Account{Subject: "auth0|ada", Email: "ada@example.com", SignedInAt: signedIn},
		EULA:    &Acceptance{Subject: "auth0|ada", AgreementID: "agreement-1", AcceptedAt: signedIn.Add(time.Minute)},
	}

	require.NoError(t, store.Save(record))
	loaded, err := store.Load()

	require.NoError(t, err)
	assert.Equal(t, record, loaded)
}

func TestStoreLoadsNothingWhenThereIsNoFile(t *testing.T) {
	record, err := NewStore(t.TempDir()).Load()

	require.NoError(t, err)
	assert.Equal(t, Record{}, record)
	assert.Equal(t, StateSignedOut, record.State())
}

func TestStoreKeepsTheRecordPrivateToTheUser(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no POSIX file modes")
	}
	home := filepath.Join(t.TempDir(), "sam")
	store := NewStore(home)

	require.NoError(t, store.Save(Record{Account: &Account{Subject: "auth0|ada"}}))

	info, err := os.Stat(filepath.Join(home, fileName))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	entries, err := os.ReadDir(home)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "no temporary file is left behind")
}

func TestStoreReportsAnUnreadableRecord(t *testing.T) {
	home := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(home, fileName), []byte("account: [not, a, map"), 0o600))

	_, err := NewStore(home).Load()

	require.Error(t, err)
}

func TestUnwritableStoreNeitherTrustsNorWritesTheWorkingDirectory(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	stray := Record{
		Account: &Account{Subject: "auth0|ada", Email: "ada@example.com"},
		EULA:    &Acceptance{Subject: "auth0|ada", AgreementID: "agreement-1"},
	}
	require.NoError(t, NewStore(".").Save(stray))
	store := UnwritableStore()

	loaded, err := store.Load()
	require.NoError(t, err)
	assert.Equal(t, StateSignedOut, loaded.State())

	require.NoError(t, os.Remove(filepath.Join(cwd, fileName)))
	require.ErrorIs(t, store.Save(stray), ErrNoStore)
	assert.NoFileExists(t, filepath.Join(cwd, fileName))
}

func TestUnlinkedNamesTheSpotDraftValuesABuildLeftOut(t *testing.T) {
	linked := Agreements{BaseURL: "https://api.us.spotdraft.com", EULAClickwrapID: "eula-1"}

	assert.Empty(t, linked.Unlinked())
	assert.Equal(t, []string{"base_url", "eula_clickwrap"}, Agreements{}.Unlinked())
}
