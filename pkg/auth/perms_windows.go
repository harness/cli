// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build windows

package auth

import (
	"fmt"
	"runtime"

	"golang.org/x/sys/windows"
)

// restrictToCurrentUser sets an explicit, non-inherited DACL on path that
// grants full access only to the current user. NTFS ignores the POSIX mode
// bits passed to os.MkdirAll/os.WriteFile, so 0700/0600 are no-ops on
// Windows — this is the actual access restriction on that platform.
func restrictToCurrentUser(path string) error {
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		return fmt.Errorf("looking up current user SID: %w", err)
	}
	sid := user.User.Sid

	var pinner runtime.Pinner
	pinner.Pin(sid)
	defer pinner.Unpin()

	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}}, nil)
	if err != nil {
		return fmt.Errorf("building ACL: %w", err)
	}

	err = windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil,
	)
	if err != nil {
		return fmt.Errorf("setting ACL on %q: %w", path, err)
	}
	return nil
}
