package runtime

import (
	"github.com/dapperlabs/bamboo-node/pkg/language/runtime/parser"
	"github.com/dapperlabs/bamboo-node/pkg/language/runtime/sema"
	. "github.com/onsi/gomega"
	"testing"
)

func TestCheckConstantAndVariableDeclarations(t *testing.T) {
	RegisterTestingT(t)

	program, errors := parser.Parse(`
        let x = 1
        var y = 1
    `)

	Expect(errors).
		To(BeEmpty())

	checker := sema.NewChecker(program)
	err := checker.Check()
	Expect(err).
		ToNot(HaveOccurred())

	Expect(checker.Globals["x"].Type).
		To(Equal(&sema.IntType{}))

	Expect(checker.Globals["y"].Type).
		To(Equal(&sema.IntType{}))
}

func TestCheckBoolean(t *testing.T) {
	RegisterTestingT(t)

	program, errors := parser.Parse(`
        let x = true
    `)

	Expect(errors).
		To(BeEmpty())

	checker := sema.NewChecker(program)
	err := checker.Check()
	Expect(err).
		ToNot(HaveOccurred())

	Expect(checker.Globals["x"].Type).
		To(Equal(&sema.BoolType{}))
}

func TestCheckInvalidVariableRedeclaration(t *testing.T) {
	RegisterTestingT(t)

	program, errors := parser.Parse(`
        fun test() {
            let x = true
            let x = false
        }
    `)

	Expect(errors).
		To(BeEmpty())

	checker := sema.NewChecker(program)
	err := checker.Check()

	Expect(err).
		To(BeAssignableToTypeOf(&sema.RedeclarationError{}))
}

func TestCheckInvalidUnknownDeclaration(t *testing.T) {
	RegisterTestingT(t)

	program, errors := parser.Parse(`
       fun test() {
           return x
       }
	`)

	Expect(errors).
		To(BeEmpty())

	checker := sema.NewChecker(program)
	err := checker.Check()

	Expect(err).
		To(BeAssignableToTypeOf(&sema.NotDeclaredError{}))
}

func TestCheckInvalidUnknownDeclarationAssignment(t *testing.T) {
	RegisterTestingT(t)

	program, errors := parser.Parse(`
      fun test() {
          x = 2
      }
	`)

	Expect(errors).
		To(BeEmpty())

	checker := sema.NewChecker(program)
	err := checker.Check()

	Expect(err).
		To(BeAssignableToTypeOf(&sema.NotDeclaredError{}))
}

func TestCheckInvalidConstantAssignment(t *testing.T) {
	RegisterTestingT(t)

	program, errors := parser.Parse(`
      fun test() {
          let x = 2
          x = 3
      }
	`)

	Expect(errors).
		To(BeEmpty())

	checker := sema.NewChecker(program)
	err := checker.Check()

	Expect(err).
		To(BeAssignableToTypeOf(&sema.AssignmentToConstantError{}))
}

func TestCheckAssignment(t *testing.T) {
	RegisterTestingT(t)

	program, errors := parser.Parse(`
      fun test() {
          var x = 2
          x = 3
      }
	`)

	Expect(errors).
		To(BeEmpty())

	checker := sema.NewChecker(program)
	err := checker.Check()

	Expect(err).
		ToNot(HaveOccurred())
}

func TestCheckInvalidGlobalConstantAssignment(t *testing.T) {
	RegisterTestingT(t)

	program, errors := parser.Parse(`
      let x = 2

      fun test() {
          x = 3
      }
	`)

	Expect(errors).
		To(BeEmpty())

	checker := sema.NewChecker(program)
	err := checker.Check()

	Expect(err).
		To(BeAssignableToTypeOf(&sema.AssignmentToConstantError{}))
}

func TestCheckGlobalVariableAssignment(t *testing.T) {
	RegisterTestingT(t)

	program, errors := parser.Parse(`
      var x = 2

      fun test(): Int64 {
          x = 3
          return x
      }
	`)

	Expect(errors).
		To(BeEmpty())

	checker := sema.NewChecker(program)
	err := checker.Check()

	Expect(err).
		ToNot(HaveOccurred())
}

func TestCheckInvalidAssignmentToParameter(t *testing.T) {
	RegisterTestingT(t)

	program, errors := parser.Parse(`
      fun test(x: Int8) {
           x = 2
      }
	`)

	Expect(errors).
		To(BeEmpty())

	checker := sema.NewChecker(program)
	err := checker.Check()

	Expect(err).
		To(BeAssignableToTypeOf(&sema.AssignmentToConstantError{}))
}

func TestCheckArrayIndexingWithInteger(t *testing.T) {
	RegisterTestingT(t)

	program, errors := parser.Parse(`
      fun test() {
          let z = [0, 3]
          z[0]
      }
	`)

	Expect(errors).
		To(BeEmpty())

	checker := sema.NewChecker(program)
	err := checker.Check()

	Expect(err).
		To(Not(HaveOccurred()))
}

func TestCheckNestedArrayIndexingWithInteger(t *testing.T) {
	RegisterTestingT(t)

	program, errors := parser.Parse(`
      fun test() {
          let z = [[0, 1], [2, 3]]
          z[0][1]
      }
	`)

	Expect(errors).
		To(BeEmpty())

	checker := sema.NewChecker(program)
	err := checker.Check()

	Expect(err).
		To(Not(HaveOccurred()))
}

func TestCheckInvalidArrayIndexingWithBool(t *testing.T) {
	RegisterTestingT(t)

	program, errors := parser.Parse(`
      fun test() {
          let z = [0, 3]
          z[true]
      }
	`)

	Expect(errors).
		To(BeEmpty())

	checker := sema.NewChecker(program)
	err := checker.Check()

	Expect(err).
		To(BeAssignableToTypeOf(&sema.NotIndexingTypeError{}))
}

func TestCheckInvalidArrayIndexingIntoBool(t *testing.T) {
	RegisterTestingT(t)

	program, errors := parser.Parse(`
      fun test(): Int64 {
          return true[0]
      }
	`)

	Expect(errors).
		To(BeEmpty())

	checker := sema.NewChecker(program)
	err := checker.Check()

	Expect(err).
		To(BeAssignableToTypeOf(&sema.NotIndexableTypeError{}))
}

func TestCheckInvalidArrayIndexingIntoInteger(t *testing.T) {
	RegisterTestingT(t)

	program, errors := parser.Parse(`
      fun test(): Int64 {
          return 2[0]
      }
	`)

	Expect(errors).
		To(BeEmpty())

	checker := sema.NewChecker(program)
	err := checker.Check()

	Expect(err).
		To(BeAssignableToTypeOf(&sema.NotIndexableTypeError{}))
}

func TestCheckInvalidArrayIndexingAssignmentWithBool(t *testing.T) {
	RegisterTestingT(t)

	program, errors := parser.Parse(`
      fun test() {
          let z = [0, 3]
          z[true] = 2
      }
	`)

	Expect(errors).
		To(BeEmpty())

	checker := sema.NewChecker(program)
	err := checker.Check()

	Expect(err).
		To(BeAssignableToTypeOf(&sema.NotIndexingTypeError{}))
}

func TestCheckArrayIndexingAssignmentWithInteger(t *testing.T) {
	RegisterTestingT(t)

	program, errors := parser.Parse(`
      fun test() {
          let z = [0, 3]
          z[0] = 2
      }
	`)

	Expect(errors).
		To(BeEmpty())

	checker := sema.NewChecker(program)
	err := checker.Check()

	Expect(err).
		To(Not(HaveOccurred()))
}

func TestCheckInvalidUnknownDeclarationIndexing(t *testing.T) {
	RegisterTestingT(t)

	program, errors := parser.Parse(`
      fun test() {
          x[0]
      }
	`)

	Expect(errors).
		To(BeEmpty())

	checker := sema.NewChecker(program)
	err := checker.Check()

	Expect(err).
		To(BeAssignableToTypeOf(&sema.NotDeclaredError{}))
}

func TestCheckInvalidUnknownDeclarationIndexingAssignment(t *testing.T) {
	RegisterTestingT(t)

	program, errors := parser.Parse(`
      fun test() {
          x[0] = 2
      }
	`)

	Expect(errors).
		To(BeEmpty())

	checker := sema.NewChecker(program)
	err := checker.Check()

	Expect(err).
		To(BeAssignableToTypeOf(&sema.NotDeclaredError{}))
}

func TestCheckInvalidParameterNameRedeclaration(t *testing.T) {
	RegisterTestingT(t)

	program, errors := parser.Parse(`
      fun test(a: Int, a: Int) {}
	`)

	Expect(errors).
		To(BeEmpty())

	checker := sema.NewChecker(program)
	err := checker.Check()

	Expect(err).
		To(BeAssignableToTypeOf(&sema.RedeclarationError{}))
}

func TestCheckInvalidArgumentLabelRedeclaration(t *testing.T) {
	RegisterTestingT(t)

	program, errors := parser.Parse(`
      fun test(x a: Int, x b: Int) {}
	`)

	Expect(errors).
		To(BeEmpty())

	checker := sema.NewChecker(program)
	err := checker.Check()

	Expect(err).
		To(BeAssignableToTypeOf(&sema.RedeclarationError{}))
}
