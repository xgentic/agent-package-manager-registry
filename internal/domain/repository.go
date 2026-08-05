package domain

// RepositoryName is the name of an independent namespace, as it appears in the
// base URL: `/api/agentpackages/{repository}` (ADR-0009).
//
// The character class is the one the APM client already allows for registry
// names, so a repository this server accepts is a registry the client can be
// configured against (FR-30).
type RepositoryName string

const maxRepositoryNameLength = 64

// ParseRepositoryName validates against `^[a-z0-9][a-z0-9._-]*$`.
func ParseRepositoryName(raw string) (RepositoryName, error) {
	switch {
	case raw == "":
		return "", invalidInput("repository", "must not be empty")
	case len(raw) > maxRepositoryNameLength:
		return "", invalidInput("repository", "must be at most %d characters", maxRepositoryNameLength)
	}

	for i, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case (r == '.' || r == '_' || r == '-') && i > 0:
		default:
			if i == 0 {
				return "", invalidInput("repository", "must start with a lowercase letter or digit")
			}
			return "", invalidInput("repository", "must contain only lowercase letters, digits, %q, %q and %q", ".", "_", "-")
		}
	}
	return RepositoryName(raw), nil
}

func (n RepositoryName) String() string { return string(n) }
