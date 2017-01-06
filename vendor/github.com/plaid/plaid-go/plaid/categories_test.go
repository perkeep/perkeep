package plaid

import (
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestCategories(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "categories tests")
}

var _ = Describe("categories", func() {

	Describe("GetCategories", func() {

		It("returns non-empty array", func() {
			categories, err := GetCategories(Tartan)
			Expect(err).To(BeNil(), "err should be nil")
			Expect(categories).ToNot(BeEmpty())
		})

	})

	Describe("GetCategory", func() {

		It("returns proper fields", func() {
			c, err := GetCategory(Tartan, "13001000")
			Expect(err).To(BeNil(), "err should be nil")
			Expect(c.Hierarchy).ToNot(BeEmpty())
			Expect(c.Hierarchy[0]).To(Equal("Food and Drink"))
			Expect(c.Hierarchy[1]).To(Equal("Bar"))
			Expect(c.ID).To(Equal("13001000"))
			Expect(c.Type).To(Equal("place"))
		})

	})

})

func ExampleGetCategory() {
	category, err := GetCategory(Tartan, "13005006")
	fmt.Println(err)
	fmt.Println(category.Hierarchy[2])
	fmt.Println(category.Type)
	// Output: <nil>
	// Sushi
	// place
}
