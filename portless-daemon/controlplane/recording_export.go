package controlplane

import (
	"context"
	"encoding/base64"
	"github.com/runportless/portless/portless-daemon/api/contract"
	"io"
)

// RecordingExportChunk returns the next bounded segment of a complete recording snapshot.
func (s *Service) RecordingExportChunk(ctx context.Context, project, environment, name string, query contract.RecordingExportChunkQuery) (contract.RecordingExportChunk, error) {
	chunk, err := s.recordingExporter.Read(ctx, project, environment, name, query.Cursor, query.MaxBytes)
	if err != nil {
		return contract.RecordingExportChunk{}, err
	}
	return contract.RecordingExportChunk{SchemaVersion: 4, Snapshot: chunk.Snapshot, Data: base64.StdEncoding.EncodeToString(chunk.Bytes), NextCursor: chunk.NextCursor, Complete: chunk.Complete}, nil
}

// WriteRecordingExport streams a complete retained snapshot without accumulating its payloads.
func (s *Service) WriteRecordingExport(ctx context.Context, project, environment, name string, writer io.Writer) error {
	return s.recordingExporter.Write(ctx, project, environment, name, writer)
}
