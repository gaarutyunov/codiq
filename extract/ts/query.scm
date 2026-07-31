; TypeScript stanza — tree-sitter query half (SPEC.md §5).
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
; This file says *where* things are. ts.go says what they are called: it builds
; the SCIP descriptor suffix by walking each captured node's ancestors, assigns
; role and symbol_kind, and resolves same-file references. Anything that needs
; another file is emitted unresolved (§4.3).

; ---------------------------------------------------------------- scopes -----
;
; Whole declaration nodes, not bodies: a method's `statement_block` is then a
; plain block scope *nested inside* the method scope, so nesting falls out of
; byte containment and no two patterns claim the same node.

(program) @scope.file
(function_declaration) @scope.function
(generator_function_declaration) @scope.function
(function_expression) @scope.function
(arrow_function) @scope.function
(method_definition) @scope.function
(class_declaration) @scope.type
(abstract_class_declaration) @scope.type
(interface_declaration) @scope.type
(enum_declaration) @scope.type
(internal_module) @scope.package
(statement_block) @scope.block

; ----------------------------------------------------------- definitions -----
;
; The file itself is the definition of a module. TypeScript writes no `package`
; clause, so this is the one definition with no identifier to hang @name on —
; the mapper names it from the coordinate and the path instead, which is where
; module identity lives in this ecosystem. It is what lets the link pass derive
; `imports` (file → file) as a plain descriptor join against the @import
; references below, exactly as it does for Go, and it is emitted with the
; neutral-core `package` kind because that is the kind link's join selects on.

(program) @definition.package

(function_declaration name: (identifier) @name) @definition.function
(generator_function_declaration name: (identifier) @name) @definition.function

(class_declaration name: (type_identifier) @name) @definition.type
(abstract_class_declaration name: (type_identifier) @name) @definition.type
(type_alias_declaration name: (type_identifier) @name) @definition.type
(enum_declaration name: (identifier) @name) @definition.type

; An interface is captured as its own kind rather than refined afterwards: the
; CST already says which it is, and link keys `implements` off `interface`.
(interface_declaration name: (type_identifier) @name) @definition.interface

(internal_module name: (identifier) @name) @definition.module

(method_definition name: (property_identifier) @name) @definition.method
(method_signature name: (property_identifier) @name) @definition.method
(abstract_method_signature name: (property_identifier) @name) @definition.method

(public_field_definition name: (property_identifier) @name) @definition.field
(property_signature name: (property_identifier) @name) @definition.field
(enum_assignment name: (property_identifier) @name) @definition.field
(enum_body (property_identifier) @name) @definition.field

(variable_declarator name: (identifier) @name) @definition.variable

(required_parameter pattern: (identifier) @name) @definition.parameter
(optional_parameter pattern: (identifier) @name) @definition.parameter
(type_parameter name: (type_identifier) @name) @definition.parameter

; --------------------------------------------------------------- imports -----
;
; @name is the quoted module specifier; the mapper trims the quotes, resolves it
; against the package coordinate, and reads the clause for the names it binds.

(import_statement source: (string) @name) @import

; ------------------------------------------------------------ references -----
;
; A call and a plain read can capture the same identifier — `console.log` yields
; both a @reference.call on `log` and a @reference.read on `console`. The mapper
; dedupes by identifier range, preferring the more specific role, and drops any
; reference landing on a node that a definition already claimed (which is how
; `(type_identifier) @reference.type` avoids re-emitting `class Loud`).

(call_expression function: (identifier) @name) @reference.call
(call_expression function: (member_expression property: (property_identifier) @name)) @reference.call

; `new C()` names a type, not a value: the constructor slot holds a plain
; identifier, so it needs its own pattern to come out as a type reference.
(new_expression constructor: (identifier) @name) @reference.type

(member_expression object: (identifier) @name) @reference.read
(member_expression property: (property_identifier) @name) @reference.read

(type_identifier) @reference.type
