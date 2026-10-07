// Copyright 2026 Candace Labs

package github

import (
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidRepository reports a remote that is not a repository on
// github.com.
var ErrInvalidRepository = errors.New("github: not a github.com repository")

// Remote is one repository on github.com, as its operations' owner and
// repo parameters name it.
type Remote struct {
	Owner string
	Name  string
}

const (
	sshPrefix     = "git@" + host + ":"
	httpsPrefix   = "https://" + host + "/"
	gitSuffix     = ".git"
	pathSeparator = "/"
)

// ParseRemote reads a git remote's URL (https or SSH, with or without
// .git) or owner/name.
func ParseRemote(remote string) (Remote, error) {
	rest := strings.TrimSpace(remote)
	switch {
	case strings.HasPrefix(rest, sshPrefix):
		rest = strings.TrimPrefix(rest, sshPrefix)
	case strings.HasPrefix(rest, httpsPrefix):
		rest = strings.TrimPrefix(rest, httpsPrefix)
	case strings.Contains(rest, "://"):
		return Remote{}, fmt.Errorf("%w: %q", ErrInvalidRepository, remote)
	}
	owner, name, found := strings.Cut(strings.TrimSuffix(strings.TrimSuffix(rest, pathSeparator), gitSuffix), pathSeparator)
	if !found || owner == "" || name == "" || strings.Contains(name, pathSeparator) {
		return Remote{}, fmt.Errorf("%w: %q", ErrInvalidRepository, remote)
	}
	return Remote{Owner: owner, Name: name}, nil
}
