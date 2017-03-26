package plaid

import (
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestInstitutions(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "institutions tests")
}

var _ = Describe("institutions", func() {

	Describe("GetInstitutions", func() {

		It("returns non-empty array", func() {
			institutions, err := GetInstitutions(Tartan)
			Expect(err).To(BeNil(), "err should be nil")
			Expect(institutions).ToNot(BeEmpty())
		})

	})

	Describe("GetInstitution", func() {

		It("returns proper fields", func() {
			i, err := GetInstitution(Tartan, "5301a9d704977c52b60000db")
			Expect(err).To(BeNil(), "err should be nil")
			Expect(i.HasMFA).To(BeFalse())
			Expect(i.ID).To(Equal("5301a9d704977c52b60000db"))
			Expect(i.MFA).To(BeEmpty())
			Expect(i.Name).To(Equal("American Express"))
			Expect(i.Type).To(Equal("amex"))
			Expect(i.Products).ToNot(BeEmpty())
			Expect(i.Products).To(ContainElement("balance"))
			Expect(i.Products).To(ContainElement("connect"))
		})

	})

})

func ExampleGetInstitution() {
	institution, err := GetInstitution(Tartan, "5301a9d704977c52b60000db")
	fmt.Println(err)
	fmt.Println(institution.Name)
	fmt.Println(institution.Type)
	// Output: <nil>
	// American Express
	// amex
}
