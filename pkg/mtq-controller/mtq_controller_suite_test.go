package mtq_controller_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestMtqController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "MtqController Suite")
}
