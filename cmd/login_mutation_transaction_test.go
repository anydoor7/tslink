package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/credentials"
	"github.com/anydoor7/tslink/internal/testwait"
)

func TestLoginRollbackCannotOverwriteConcurrentCredentialWriter(t *testing.T) {
	setupLoginTest(t)
	loginMutationTransactionFn = credentials.WithMutationTransaction
	mockAPIKeySuccess(t)
	if err := credentials.SetAPIKey("tskey-api-<test-only-before>"); err != nil {
		t.Fatal(err)
	}
	writeMeta := loginWriteSlotMetaFn
	t.Cleanup(func() { loginWriteSlotMetaFn = writeMeta })
	metadataReached := make(chan struct{})
	resumeRollback := make(chan struct{})
	loginWriteSlotMetaFn = func(slot string, meta credentials.SlotMetadata) error {
		close(metadataReached)
		<-resumeRollback
		return errors.New("synthetic metadata write failure")
	}
	commitDone := make(chan error, 1)
	go func() {
		_, err := commitLoginCredential(context.Background(), defaultLoginCredentialStore{}, loginCredentialModeAPIKey, "tskey-api-<test-only-candidate>", loginReplaceOptions{})
		commitDone <- err
	}()
	testwait.Recv(t, metadataReached, "login reached the metadata failure boundary")
	writerDone := make(chan error, 1)
	go func() {
		_, err := credentials.SetAPIKeyWithBackend("tskey-api-<test-only-after-rollback>")
		writerDone <- err
	}()
	select {
	case err := <-writerDone:
		close(resumeRollback)
		t.Fatalf("concurrent credential writer completed inside login rollback transaction: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(resumeRollback)
	if err := testwait.Recv(t, commitDone, "login rollback finished"); err == nil || !strings.Contains(err.Error(), "synthetic metadata write failure") {
		t.Fatalf("login did not fail through the intended rollback path: %v", err)
	}
	if err := testwait.Recv(t, writerDone, "concurrent credential writer finished after rollback"); err != nil {
		t.Fatalf("concurrent credential writer failed after rollback: %v", err)
	}
	stored, err := credentials.GetAPIKey()
	if err != nil || stored != "tskey-api-<test-only-after-rollback>" {
		t.Fatalf("rollback overwrote later writer: later_writer_preserved=%v err=%v", stored == "tskey-api-<test-only-after-rollback>", err)
	}
}
