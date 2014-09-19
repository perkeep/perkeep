package iterator_test

import (
	"testing"

	. "camlistore.org/third_party/github.com/onsi/ginkgo"
	. "camlistore.org/third_party/github.com/onsi/gomega"

	"camlistore.org/third_party/github.com/syndtr/goleveldb/leveldb/testutil"
)

func TestIterator(t *testing.T) {
	testutil.RunDefer()

	RegisterFailHandler(Fail)
	RunSpecs(t, "Iterator Suite")
}
