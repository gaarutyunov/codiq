; C stanza — tree-sitter query half, C dialect (SPEC.md §5).
;
; Captures use the standard vocabulary shared by every language:
;
;   @definition.<kind>  a definition occurrence
;   @reference.<role>   a reference occurrence
;   @scope.<kind>       a lexical scope
;   @import             an import occurrence
;   @name               the identifier used for the descriptor and `name`
;
; …and **one addition, `@declaration`**, which is this stanza's deviation from
; §5 and is forced by the language rather than chosen. §5's vocabulary assumes
; the query can say whether a captured site defines or references a symbol. In
; C it often cannot:
;
;   * `void greet();` and `void greet() { … }` are a `declaration` and a
;     `function_definition` — different node kinds, so those two the query *can*
;     tell apart.
;   * But a `declaration` may be a function prototype (a reference to something
;     defined elsewhere), an `extern` variable declaration (likewise), a local
;     variable (a definition) or a file-scope variable (a definition), and which
;     one it is depends on the *shape of its declarator chain* and on its
;     storage class. C's declarator syntax nests the name arbitrarily deep —
;     `char *(*table[4])(void)` — so there is no field path a pattern can name
;     and no bounded set of patterns that covers it.
;
; So `(declaration) @declaration` captures the site and cc.go decides both the
; role and the kind by walking the declarator. The same applies to the
; `struct`/`union`/`enum` specifiers, where a body is the only thing separating
; a definition from a forward declaration; those keep their `@definition.type`
; capture and cc.go downgrades the bodyless ones, because there the *kind* is
; never in doubt.
;
; cc.go's package comment argues the header/source rule this rests on: **a
; declarator with a body is a definition; one without is a reference carrying
; the same descriptor.** It is what makes `greeter.h` resolve into `greeter.c`
; instead of competing with it.

; ---------------------------------------------------------------- scopes -----
;
; C has real block scope, which is the first time in this graph that
; `(compound_statement)` earns a capture: PHP and Ruby have none, Python has
; none, and Go's braces are already the function's. A name declared inside `{ }`
; is invisible outside it, and cc.go resolves same-file references by asking
; which scope a definition was declared in.
;
; There is no `@scope.package`. C has no namespace of any kind — every external
; symbol lives in one flat namespace shared by the whole program — and that is
; not an omission to be papered over with the directory. It is the single fact
; the descriptors in this dialect are built from; see cc.go.

(translation_unit) @scope.file
(function_definition) @scope.function
(compound_statement) @scope.block
(struct_specifier body: (field_declaration_list)) @scope.type
(union_specifier body: (field_declaration_list)) @scope.type
(enum_specifier body: (enumerator_list)) @scope.type

; ----------------------------------------------------------- definitions -----
;
; The file is the definition of a package, as in every language here. For C the
; package is the *global* namespace and its suffix is empty, which is the honest
; rendering of "C has no module system": every C file in a package declares the
; same one namespace, and every symbol with external linkage is in it.
;
; The kind is `package` and never `module`. `imports` joins on
; `symbol_kind = 'package'` (store/sqlc/query.sql), so a `module` derives no
; edge and does so *silently* — the defect TypeScript still carries.
; cc_test.go guards it by name.
;
; cc.go emits a second family of `package` definitions from the same node: one
; per suffix of the file's own path. That is `#include`'s only chance of
; deriving an edge, and the query cannot express it because the path is not in
; the CST at all.

(translation_unit) @definition.package

; Types. A specifier with a body defines; one without forward-declares, and
; cc.go tells them apart by asking for the `body` field.
;
; `typedef` is the fourth: `typedef struct Greeter { … } Greeter;` states a tag
; and an alias, and in C those are two symbols in two different name spaces that
; this graph has one descriptor for. cc.go emits the first and drops the
; duplicate.
(struct_specifier name: (type_identifier) @name) @definition.type
(union_specifier name: (type_identifier) @name) @definition.type
(enum_specifier name: (type_identifier) @name) @definition.type
(type_definition declarator: (type_identifier) @name) @definition.type
(enumerator name: (identifier) @name) @definition.constant

; Callables and state. A `function_definition` has a body by construction, so it
; is the one shape in C that is unambiguously a definition.
(function_definition) @definition.function
(field_declaration) @definition.field
(parameter_declaration) @definition.parameter
(declaration) @declaration

; The preprocessor, which is the only part of C that declares names the compiler
; never sees as declarations. A macro has no scope, no namespace and no type: it
; is a token substitution keyed on a name, so its descriptor sits at the global
; namespace even when the `#define` is written inside a block. cc.go says what
; that costs.
(preproc_def name: (identifier) @name) @definition.constant
(preproc_function_def name: (identifier) @name) @definition.function

; --------------------------------------------------------------- imports -----
;
; `#include` is the whole of C's import story and it is not an import of a
; *name*: it pastes a file in. There is no alias table, nothing analogous to
; PHP's `use`, and the path is resolved by the build system's `-I` search and
; not by anything in the source.
;
; cc.go approximates that search the only way a file-local reader can — see its
; package comment — and this is where the path is handed over.
(preproc_include path: (string_literal) @name) @import
(preproc_include path: (system_lib_string) @name) @import

; ------------------------------------------------------------ references -----
;
; Deliberately narrow, and narrower than it could be. A bare `(identifier)` in
; an expression is a local read, a global read, an enum constant or an
; object-like macro use, and nothing around it says which — so it is not
; captured, exactly as php.go declines to capture a bare `(name)`. The cost is
; stated in cc.go: an object-like macro use resolves to nothing. A
; *function-like* macro use is a `call_expression` and does resolve, which is
; the asymmetry the preprocessor hands us.
(call_expression function: (identifier) @name) @reference.call
(call_expression function: (field_expression field: (field_identifier) @name)) @reference.call
(field_expression field: (field_identifier) @name) @reference.read

; A type name in any position. The struct tag in a definition is a
; `type_identifier` too, and cc.go's claim table drops the reference that lands
; on bytes a definition already owns.
(type_identifier) @reference.type
