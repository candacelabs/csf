#include <caml/alloc.h>
#include <caml/memory.h>
#include <caml/mlvalues.h>

typedef struct TSLanguage TSLanguage;
const TSLanguage *tree_sitter_python(void);

/* The pinned upstream grammar owns Python syntax and indentation. */
CAMLprim value candace_tree_sitter_python(value unit) {
  CAMLparam1(unit);
  CAMLreturn(caml_copy_nativeint((intnat)tree_sitter_python()));
}
