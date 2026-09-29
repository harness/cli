// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/spec"
)

type mutationFlagLog struct {
	records []cmdctx.FieldMutation
}

type mutationFlagValue struct {
	pflag.Value
	slice pflag.SliceValue
	kind  cmdctx.MutationKind
	log   *mutationFlagLog
}

func (v *mutationFlagValue) Set(raw string) error {
	if err := v.Value.Set(raw); err != nil {
		return err
	}
	v.log.records = append(v.log.records, cmdctx.FieldMutation{Kind: v.kind, Raw: raw})
	return nil
}

func (v *mutationFlagValue) Append(raw string) error {
	if err := v.slice.Append(raw); err != nil {
		return err
	}
	v.log.records = append(v.log.records, cmdctx.FieldMutation{Kind: v.kind, Raw: raw})
	return nil
}

func (v *mutationFlagValue) Replace(raw []string) error {
	if err := v.slice.Replace(raw); err != nil {
		return err
	}
	records := v.log.records[:0]
	for _, record := range v.log.records {
		if record.Kind != v.kind {
			records = append(records, record)
		}
	}
	v.log.records = records
	for _, value := range raw {
		v.log.records = append(v.log.records, cmdctx.FieldMutation{Kind: v.kind, Raw: value})
	}
	return nil
}

func (v *mutationFlagValue) GetSlice() []string { return v.slice.GetSlice() }

func registerMutationFlags(cmd *cobra.Command, cs *spec.CommandSpec) {
	if !cs.BuiltinFlags.Set && !cs.BuiltinFlags.Del {
		return
	}
	log := &mutationFlagLog{}
	register := func(name, usage string, kind cmdctx.MutationKind) {
		cmd.Flags().StringArray(name, nil, usage)
		flag := cmd.Flags().Lookup(name)
		flag.Value = &mutationFlagValue{Value: flag.Value, slice: flag.Value.(pflag.SliceValue), kind: kind, log: log}
	}
	if cs.BuiltinFlags.Set {
		register("set", "Set a field as key=value or a set member as field.member (repeatable)", cmdctx.MutationSet)
		register("add", "Add a field member without replacing an existing value (repeatable)", cmdctx.MutationAdd)
	}
	if cs.BuiltinFlags.Set || cs.BuiltinFlags.Del {
		register("del", "Delete a field or field member (repeatable)", cmdctx.MutationDelete)
	}
}

func capturedMutationFlags(cmd *cobra.Command) []cmdctx.FieldMutation {
	for _, name := range []string{"set", "del"} {
		if flag := cmd.Flags().Lookup(name); flag != nil {
			if value, ok := flag.Value.(*mutationFlagValue); ok {
				return append([]cmdctx.FieldMutation(nil), value.log.records...)
			}
		}
	}
	return nil
}
