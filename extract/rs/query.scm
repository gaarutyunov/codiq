; Rust stanza — tree-sitter query half (SPEC.md §5).
;
; Captures use the standard vocabulary shared by every language, which is where
; normalisation to the neutral core happens:
;
;   @definition.<kind>  a definition occurrence
;   @reference.<role>   a reference occurrence
;   @scope.<kind>       a lexical scope
;   @import             an import occurrence
;   @name               the identifier used for the descriptor and `name`
;
; This file says *where* things are. rs.go says what they are called: it builds
; the SCIP descriptor suffix by walking each captured node's ancestors, assigns
; role and symbol_kind, and resolves same-file references. Anything that needs
; another file is emitted unresolved (§4.3).

; ---------------------------------------------------------------- scopes -----
;
; Rust's lexical scopes, which are the C-family ones and not Python's: a `block`
; really does scope its bindings, so it is captured here exactly as the Go
; stanza captures `(block)`.
;
; `mod_item` is a scope in a way no earlier language's unit of modularity was.
; Go's package is a directory and TypeScript's and Python's module is a file, so
; in all three the module scope and the file scope are the same node. Rust
; writes `mod foo { … }` *inside* a file, so a module can be a scope strictly
; below the file — which is why ScopePackage finally has a use.
;
; `impl_item` is a scope too, and it is the answer to "where does an `impl`
; block live in a model with no `impl` vocabulary": it is a lexical region that
; groups members of a type, which is what `@scope.type` already means.

(source_file) @scope.file
(mod_item body: (declaration_list)) @scope.package
(function_item) @scope.function
(function_signature_item) @scope.function
(closure_expression) @scope.function
(struct_item) @scope.type
(enum_item) @scope.type
(union_item) @scope.type
(trait_item) @scope.type
(impl_item) @scope.type
(block) @scope.block

; ----------------------------------------------------------- definitions -----
;
; The file itself is the definition of a module, as in TypeScript and Python and
; for the same reason: Rust writes no clause naming the module a file is, so this
; is the one definition with no identifier to hang @name on and rs.go names it
; from the coordinate and the path. It carries the neutral-core `package` kind
; because that is the kind link's `imports` derivation joins on.

(source_file) @definition.package

; `mod foo { … }` — the *inline* form, which declares a module inside this file
; and is therefore a definition. The body field is what tells it from `mod foo;`,
; which declares that a module lives in another file and is an import (below).
(mod_item name: (identifier) @name body: (declaration_list)) @definition.package

; Free functions, inherent methods, trait methods and trait method signatures.
; Rust has no distinct node for a method — a `function_item` inside an `impl` or
; a `trait` body is one — so the distinction is drawn in rs.go's refineKind from
; the enclosing container, exactly as the Python stanza draws it for a `def`
; inside a class.
(function_item name: (identifier) @name) @definition.function
(function_signature_item name: (identifier) @name) @definition.function

; The type-declaring items. `trait` is captured as a plain type and promoted to
; the neutral-core `interface` kind by refineKind — which for Rust is not the
; recognition problem it is in Python, where a class had to be inspected for a
; Protocol base: `trait` is a keyword and means exactly one thing.
(struct_item name: (type_identifier) @name) @definition.type
(enum_item name: (type_identifier) @name) @definition.type
(union_item name: (type_identifier) @name) @definition.type
(type_item name: (type_identifier) @name) @definition.type
(trait_item name: (type_identifier) @name) @definition.type

; Struct and union fields, and enum variants. A variant is a member of the type
; it is declared in and is reached as `Mood::Happy`, which is the same descriptor
; shape a field has, so it is a field.
(field_declaration name: (field_identifier) @name) @definition.field
(enum_variant name: (identifier) @name) @definition.field

(const_item name: (identifier) @name) @definition.constant
(static_item name: (identifier) @name) @definition.constant

; Bindings. Only the irrefutable single-identifier forms: a destructuring
; pattern binds several names at once and none of them is the declaration's
; name, so admitting one would put a descriptor on a symbol the syntax never
; named.
(let_declaration pattern: (identifier) @name) @definition.variable
(for_expression pattern: (identifier) @name) @definition.variable

; Parameters, of both a function and a closure — `parameter` is the same node in
; either. `self` is deliberately absent: it is a keyword rather than a name in
; scope (the grammar gives it its own `self_parameter` node), and rs.go resolves
; it from the enclosing `impl` instead.
(parameter pattern: (identifier) @name) @definition.parameter
(type_parameter name: (type_identifier) @name) @definition.parameter

; --------------------------------------------------------------- imports -----
;
; Three statements name something outside the file, and @name is the node rs.go
; reads the path off.
;
;   * `use a::b::C;` binds a symbol (or, with a `{…}` list, several) of another
;     module. This is Rust's `from … import …`.
;   * `mod foo;` says the module `foo` is in another file. It has no counterpart
;     in any earlier language — nothing in Go, TypeScript or Python has to
;     *declare* that a sibling file exists — and it is what an `imports` edge
;     between two Rust files is derived from.
;   * `extern crate legacy;` is the 2015-edition spelling of "this crate depends
;     on that one". Rare now, and free to support.
;
; The `mod` pattern matches the inline form as well; rs.go drops a match whose
; node carries a body, because that one is the definition captured above.

(use_declaration argument: (_) @name) @import
(mod_item name: (identifier) @name) @import
(extern_crate_declaration name: (identifier) @name) @import

; ------------------------------------------------------------ references -----
;
; A call and a plain read can capture the same identifier — `Greeter::new()`
; yields both a @reference.call and a @reference.read on `new`. rs.go dedupes by
; identifier range, preferring the more specific role, and drops any reference
; landing on a node a definition already claimed (which is how the bare
; `(type_identifier)` pattern below avoids re-emitting `struct Greeter`).
;
; Rust has two member operators and they mean different things, so both are here:
; `.` reaches a field or a method of a *value* (`field_expression`), while `::`
; reaches an item of a *type or module* (`scoped_identifier`). rs.go tells the
; qualifier side from the member side by which field of the parent the captured
; node sits in.

(call_expression function: (identifier) @name) @reference.call
(call_expression function: (field_expression field: (field_identifier) @name)) @reference.call
(call_expression function: (scoped_identifier name: (identifier) @name)) @reference.call

(field_expression value: (identifier) @name) @reference.read
(field_expression field: (field_identifier) @name) @reference.read
(scoped_identifier path: (identifier) @name) @reference.read
(scoped_identifier name: (identifier) @name) @reference.read

; Types, in every position: an annotation, a bound, a return type, a struct
; literal's constructor, and the two halves of an `impl`. `Self` is one of these
; and rs.go resolves it to the type the enclosing `impl` is for.
(type_identifier) @reference.type
(scoped_type_identifier name: (type_identifier) @name) @reference.type
(struct_expression name: (type_identifier) @name) @reference.type
