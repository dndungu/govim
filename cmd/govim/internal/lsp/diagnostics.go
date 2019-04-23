// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lsp

import (
	"context"

	"github.com/myitcv/govim/cmd/govim/internal/lsp/cache"
	"github.com/myitcv/govim/cmd/govim/internal/lsp/protocol"
	"github.com/myitcv/govim/cmd/govim/internal/lsp/source"
	"github.com/myitcv/govim/cmd/govim/internal/span"
)

func (s *Server) cacheAndDiagnose(ctx context.Context, uri span.URI, content string) error {
	s.log.Debugf(ctx, "cacheAndDiagnose: %s", uri)

	view := s.findView(ctx, uri)
	if err := view.SetContent(ctx, uri, []byte(content)); err != nil {
		return err
	}

	s.log.Debugf(ctx, "cacheAndDiagnose: set content for %s", uri)

	go func() {
		ctx := view.BackgroundContext()
		if ctx.Err() != nil {
			s.log.Errorf(ctx, "canceling diagnostics for %s: %v", uri, ctx.Err())
			return
		}

		s.log.Debugf(ctx, "cacheAndDiagnose: going to get diagnostics for %s", uri)

		reports, err := source.Diagnostics(ctx, view, uri)
		if err != nil {
			s.log.Errorf(ctx, "failed to compute diagnostics for %s: %v", uri, err)
			return
		}

		s.undeliveredMu.Lock()
		defer s.undeliveredMu.Unlock()

		s.log.Debugf(ctx, "cacheAndDiagnose: publishing diagnostics")

		for uri, diagnostics := range reports {
			if err := s.publishDiagnostics(ctx, view, uri, diagnostics); err != nil {
				if s.undelivered == nil {
					s.undelivered = make(map[span.URI][]source.Diagnostic)
				}
				s.undelivered[uri] = diagnostics
				continue
			}
			// In case we had old, undelivered diagnostics.
			delete(s.undelivered, uri)
		}

		s.log.Debugf(ctx, "cacheAndDiagnose: publishing undelivered diagnostics")

		// Anytime we compute diagnostics, make sure to also send along any
		// undelivered ones (only for remaining URIs).
		for uri, diagnostics := range s.undelivered {
			s.publishDiagnostics(ctx, view, uri, diagnostics)

			// If we fail to deliver the same diagnostics twice, just give up.
			delete(s.undelivered, uri)
		}
	}()

	s.log.Debugf(ctx, "cacheAndDiagnose: done computing diagnostics for %s", uri)

	return nil
}

func (s *Server) publishDiagnostics(ctx context.Context, view *cache.View, uri span.URI, diagnostics []source.Diagnostic) error {
	protocolDiagnostics, err := toProtocolDiagnostics(ctx, view, diagnostics)
	if err != nil {
		return err
	}
	s.client.PublishDiagnostics(ctx, &protocol.PublishDiagnosticsParams{
		Diagnostics: protocolDiagnostics,
		URI:         protocol.NewURI(uri),
	})
	return nil
}

func toProtocolDiagnostics(ctx context.Context, v source.View, diagnostics []source.Diagnostic) ([]protocol.Diagnostic, error) {
	reports := []protocol.Diagnostic{}
	for _, diag := range diagnostics {
		_, m, err := newColumnMap(ctx, v, diag.Span.URI())
		if err != nil {
			return nil, err
		}
		var severity protocol.DiagnosticSeverity
		switch diag.Severity {
		case source.SeverityError:
			severity = protocol.SeverityError
		case source.SeverityWarning:
			severity = protocol.SeverityWarning
		}
		rng, err := m.Range(diag.Span)
		if err != nil {
			return nil, err
		}
		reports = append(reports, protocol.Diagnostic{
			Message:  diag.Message,
			Range:    rng,
			Severity: severity,
			Source:   diag.Source,
		})
	}
	return reports, nil
}
