package mtq_operator_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestMtqOperator(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "MtqOperator Suite")
}
