package mtq_controller_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"testing"
)

func TestMtqControllerApp(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "MtqController Suite")
}
