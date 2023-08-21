package vmmrq_controller_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestVmmrqController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "VmmrqController Suite")
}
