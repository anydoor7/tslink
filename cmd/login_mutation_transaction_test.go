package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/credentials"
)

func TestLoginRollbackCannotOverwriteConcurrentCredentialWriter(t *testing.T) {
	setupLoginTest(t)
	loginMutationTransactionFn = credentials.WithMutationTransaction
	mockAPIKeySuccess(t)
	if err := credentials.SetAPIKey("tskey-api-before"); err != nil {
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
		_, err := commitLoginCredential(context.Background(), defaultLoginCredentialStore{}, loginCredentialModeAPIKey, "tskey-api-candidate", loginReplaceOptions{})
		commitDone <- err
	}()
	select {
	case <-metadataReached:
	case <-time.After(3 * time.Second):
		t.Fatal("login did not reach the metadata failure boundary")
	}
	writerDone := make(chan error, 1)
	go func() {
		_, err := credentials.SetAPIKeyWithBackend("tskey-api-after-rollback")
		writerDone <- err
	}()
	select {
	case err := <-writerDone:
		close(resumeRollback)
		t.Fatalf("concurrent credential writer completed inside login rollback transaction: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(resumeRollback)
	select {
	case err := <-commitDone:
		if err == nil || !strings.Contains(err.Error(), "synthetic metadata write failure") {
			t.Fatalf("login did not fail through the intended rollback path: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("login rollback did not finish")
	}
	select {
	case err := <-writerDone:
		if err != nil {
			t.Fatalf("concurrent credential writer failed after rollback: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("concurrent credential writer remained blocked")
	}
	stored, err := credentials.GetAPIKey()
	if err != nil || stored != "tskey-api-after-rollback" {
		t.Fatalf("rollback overwrote later writer: later_writer_preserved=%v err=%v", stored == "tskey-api-after-rollback", err)
	}
}
