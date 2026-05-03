package exit

import "os"

const (
	Success                   = 0
	General                   = 1
	Misuse                    = 2
	ConfigurationError        = 3
	TokenResolutionFailure    = 4
	UpstreamError             = 125
)

func Exit(code int) {
	os.Exit(code)
}

func ExitWithError(code int, err error) {
	if err != nil {
		os.Exit(code)
	}
	os.Exit(code)
}