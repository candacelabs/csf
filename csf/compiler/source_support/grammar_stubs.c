#include <caml/alloc.h>
#include <caml/memory.h>
#include <caml/mlvalues.h>

typedef struct TSLanguage TSLanguage;
const TSLanguage *tree_sitter_go(void);

/* The generated upstream grammar owns parsing; this stub only passes a pointer. */
CAMLprim value candace_tree_sitter_go(value unit) {
  CAMLparam1(unit);
  CAMLreturn(caml_copy_nativeint((intnat)tree_sitter_go()));
}
