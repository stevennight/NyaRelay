package node

import "os"

func osHostname() (string, error) {
	return os.Hostname()
}
