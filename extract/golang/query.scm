; Go stanza — tree-sitter query half (SPEC.md §5).
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
; This file says *where* things are. golang.go says what they are called: it
; builds the SCIP descriptor suffix by walking each captured node's ancestors,
; assigns role and symbol_kind, and resolves same-file references. Anything that
; needs another file is emitted unresolved (§4.3).

; ---------------------------------------------------------------- scopes -----
;
; Whole declaration nodes, not bodies: a function body `block` is then a plain
; block scope *nested inside* the function scope, so nesting falls out of byte
; containment and no two patterns claim the same node.

(source_file) @scope.file
(function_declaration) @scope.function
(method_declaration) @scope.function
(func_literal) @scope.function
(struct_type) @scope.type
(interface_type) @scope.type
(block) @scope.block

; ----------------------------------------------------------- definitions -----
;
; The package clause is a definition of the *package*, whose descriptor is the
; namespace. Every file in a Go package declares it, so several files legally
; produce the same descriptor — which is correct, and is what lets the link pass
; derive `imports` (file → file) as a plain descriptor join between an @import
; reference and these definitions, with no path arithmetic.

(package_clause (package_identifier) @name) @definition.package

(function_declaration name: (identifier) @name) @definition.function
(method_declaration name: (field_identifier) @name) @definition.method
(method_elem name: (field_identifier) @name) @definition.method
(type_spec name: (type_identifier) @name) @definition.type
(type_alias name: (type_identifier) @name) @definition.type
(field_declaration name: (field_identifier) @name) @definition.field
(const_spec name: (identifier) @name) @definition.constant
(var_spec name: (identifier) @name) @definition.variable
(short_var_declaration left: (expression_list (identifier) @name)) @definition.variable
(parameter_declaration name: (identifier) @name) @definition.parameter
(type_parameter_declaration name: (identifier) @name) @definition.parameter

; --------------------------------------------------------------- imports -----
;
; @name is the quoted path literal; the mapper trims the quotes and reads the
; optional alias from the spec's `name:` field.

(import_spec path: (interpreted_string_literal) @name) @import

; ------------------------------------------------------------ references -----
;
; A call and a plain read can capture the same identifier — `fmt.Println` yields
; both a @reference.call on `Println` and a @reference.read on `fmt`. The mapper
; dedupes by identifier range, preferring the more specific role, and drops any
; reference landing on a node that a definition already claimed (which is how
; `(type_identifier) @reference.type` avoids re-emitting `type Greeter`).

(call_expression function: (identifier) @name) @reference.call
(call_expression function: (selector_expression field: (field_identifier) @name)) @reference.call
(selector_expression operand: (identifier) @name) @reference.read
(selector_expression field: (field_identifier) @name) @reference.read
(type_identifier) @reference.type
