//go:build enrollmenttest

package server

import "context"

// Test-only bridge exposes the actual enrollment loop without replacing it.
type enrollmentTestNode struct {
	tsnetServer
	lc *LocalClient
}

func (n enrollmentTestNode) Start() error                       { return nil }
func (n enrollmentTestNode) LocalClient() (*LocalClient, error) { return n.lc, nil }
func WaitForEnrollmentTest(ctx context.Context, fn AuthHandoffFunc, name string, lc *LocalClient) error {
	s := &Server{authHandoffFn: fn}
	_, err := s.waitForInteractiveNode(ctx, enrollmentTestNode{lc: lc}, name)
	return err
}
