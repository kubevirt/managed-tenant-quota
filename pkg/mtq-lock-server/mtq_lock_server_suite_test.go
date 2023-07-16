package mtq_lock_server_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestMtqLockServer(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "MtqLockServer Suite")
}
