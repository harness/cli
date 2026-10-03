// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package fme

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/harness/cli/v3/pkg/cmdctx"
)

const (
	resolveKeysFileFnID    = "resolve_keys_file"
	validateSegmentKeysID  = "validate_segment_keys"
	maxSegmentKeysPerCall  = 10000
	keysFileStdin          = "-"
	keysFileSeparatorBlock = "\n"
)

// parseKeys reads membership keys from r. Every cell of every row is one key, so a
// one-key-per-line file, a single comma-separated row, and a multi-column grid all
// work. Quoted cells may contain commas. Cells are trimmed, empties dropped, and
// duplicates removed in first-seen order. There is no header handling: the file must
// contain only keys.
func parseKeys(r io.Reader) ([]string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))

	cr := csv.NewReader(bytes.NewReader(data))
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	cr.TrimLeadingSpace = true

	seen := map[string]bool{}
	var keys []string
	for {
		record, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("could not parse keys file: %w", err)
		}
		for _, cell := range record {
			key := strings.TrimSpace(cell)
			if key == "" || seen[key] {
				continue
			}
			if strings.ContainsAny(key, "\r\n") {
				return nil, fmt.Errorf("key %q contains a line break; keys must be single-line", key)
			}
			seen[key] = true
			keys = append(keys, key)
		}
	}
	return keys, nil
}

// resolveKeysFile loads --keys-file (a path, or "-" for stdin) and returns the parsed
// keys newline-joined; the spec splits them back into the request's keys array.
func resolveKeysFile(_ *cmdctx.Ctx, raw string) (*cmdctx.FlagResolveResult, error) {
	var (
		keys []string
		err  error
	)
	if raw == keysFileStdin {
		keys, err = parseKeys(os.Stdin)
	} else {
		f, openErr := os.Open(raw)
		if openErr != nil {
			return nil, fmt.Errorf("cannot read keys file: %w", openErr)
		}
		defer f.Close()
		keys, err = parseKeys(f)
	}
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("no keys found in %q: expected one key per line, or comma-separated keys, with no header row", raw)
	}
	return &cmdctx.FlagResolveResult{Value: strings.Join(keys, keysFileSeparatorBlock)}, nil
}

// validateSegmentKeys rejects an empty key list (unless replacing, which clears the
// segment) and lists over the per-call limit before anything is sent.
func validateSegmentKeys(_ *cmdctx.Ctx, req cmdctx.EndpointRequest) error {
	count := 0
	switch body := req.Body.(type) {
	case map[string]any:
		switch keys := body["keys"].(type) {
		case []string:
			count = len(keys)
		case []any:
			count = len(keys)
		}
	}
	if count > maxSegmentKeysPerCall {
		return fmt.Errorf("%d keys exceeds the limit of %d per call; split them across several calls", count, maxSegmentKeysPerCall)
	}
	if count == 0 && req.QueryParams["replace"] != "true" {
		return errors.New("no keys given: pass --key, or --keys-file <path>")
	}
	return nil
}
