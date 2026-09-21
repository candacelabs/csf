package patience

// BudgetInterval exposes the unexported Budget.interval to the external
// patience_test package. It exists so the suite can stay in package
// patience_test and dot-import both ginkgo and gomega (the CS-11 convention):
// an in-package test could not, because this package declares Consistently and
// a dot import of gomega — which also exports Consistently — would be a
// redeclaration. interval stays unexported in the production surface; this is
// the standard export_test.go seam for reaching it from a black-box test.
var BudgetInterval = Budget.interval
