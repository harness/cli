// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package code

import "github.com/harness/cli/pkg/registry"

const (
	mergePRBodyFnID               = "merge_pr_body"
	createPRCommentBodyFnID       = "create_pr_comment_body"
	createPRBodyFnID              = "create_pr_body"
	resolvePrincipalIDFnID        = "resolve_principal_id"
	prConversationTextFormatterID = "pr_conversation_text"
)

func ModuleInit(reg registry.ModuleRegistrar) {
	reg.RegisterBodyFn(mergePRBodyFnID, mergePRBodyFn)
	reg.RegisterBodyFn(createPRCommentBodyFnID, createPRCommentBodyFn)
	reg.RegisterBodyFn(createPRBodyFnID, createPRBodyFn)
	reg.RegisterQueryParamsFn(listMinePRQueryParamsFnID, listMinePRQueryParamsFn)
	reg.RegisterQueryParamsFn(reviewPendingPRQueryParamsFnID, reviewPendingPRQueryParamsFn)
	reg.RegisterFetchFn(listMinePRFetchFnID, listMinePRFetchFn)
	reg.RegisterFetchFn(codeownersPRFetchFnID, codeownersPRFetchFn)
	reg.RegisterFlagResolveFn(resolvePrincipalIDFnID, resolvePrincipalID)
	reg.RegisterBodyFn(reviewPRBodyFnID, reviewPRBodyFn)
	reg.RegisterWorkflow(getPRWorkflowID, GetPRWorkflow)
	reg.RegisterWorkflow(pullRepositoryWorkflowID, pullRepositoryWorkflow)
	reg.RegisterWorkflow(pullPRWorkflowID, pullPRWorkflow)
	reg.RegisterTextFormatter(reviewGroupTextFormatterID, reviewGroupTextFormatter)
	reg.RegisterTextFormatter(insightTextFormatterID, insightTextFormatter)
	reg.RegisterTextFormatter(prConversationTextFormatterID, prConversationTextFormatter)
}
