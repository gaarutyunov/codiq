; C++ stanza — tree-sitter query half, C++ dialect (SPEC.md §5).
;
; This is query_c.scm's vocabulary — including its one addition, `@declaration`,
; whose reason that file gives in full — over the C++ grammar, which is a
; different `*sitter.Language` and so needs its own compiled query even for the
; patterns the two share. Duplication is unavoidable: a query naming
; `namespace_definition` cannot compile against the C grammar, and one naming
; only the C subset would throw away every C++ construct.
;
; This query also reads **every `.h` in the corpus**, and that is the decision
; with the widest blast radius in this stanza. A `.h` may be a C header, a C++
; header, or one written for both behind `#ifdef __cplusplus`, and nothing in
; the file reliably says which. Measured on realistic input, a C header parsed
; by the C++ grammar produces ERROR=0 MISSING=0 — the identical tree, node for
; node — while a C++ header parsed by the C grammar produces 31 ERROR and 5
; MISSING nodes and loses every class, template and namespace in it. So the
; ambiguity is resolved towards C++ and the cost is close to nil; cc.go states
; the residue.

; ---------------------------------------------------------------- scopes -----
;
; C++ adds two lexical regions to C's: the namespace, which is a real scope and
; not — as in PHP and C# — a property of the file, and the lambda, which is a
; function boundary that captures rather than closing over.

(translation_unit) @scope.file
(namespace_definition) @scope.package
(function_definition) @scope.function
(lambda_expression) @scope.function
(compound_statement) @scope.block
(class_specifier body: (field_declaration_list)) @scope.type
(struct_specifier body: (field_declaration_list)) @scope.type
(union_specifier body: (field_declaration_list)) @scope.type
(enum_specifier body: (enumerator_list)) @scope.type

; ----------------------------------------------------------- definitions -----
;
; The file is the definition of a package — the global namespace, empty suffix —
; and every `namespace X { … }` is another. Kind `package`, never `module`; see
; query_c.scm.
;
; An *unnamed* `namespace { … }` is captured too and has no name to hang an
; occurrence on. It is C++'s spelling of C's `static`: everything in it has
; internal linkage, so cc.go gives its members the file's own key rather than a
; namespace, and no occurrence is emitted for the namespace itself because there
; is no name that another file could ever write.

(translation_unit) @definition.package
(namespace_definition name: (namespace_identifier) @name) @definition.package

; Types. A specifier with a body defines and one without forward-declares;
; cc.go asks for the `body` field.
;
; `class`, `struct` and `union` are all captured as `type` and cc.go promotes
; some of them to `interface`. C++ has no `interface` keyword, but it has the
; thing: a class whose member functions are all pure virtual and which declares
; no data members is an abstract base class, which is what every C++ codebase
; writes where Java writes `interface`. The promotion is what makes link's
; method-set containment derivation — keyed on `symbol_kind = 'interface'` —
; fire at all in this language.
(class_specifier name: (type_identifier) @name) @definition.type
(struct_specifier name: (type_identifier) @name) @definition.type
(union_specifier name: (type_identifier) @name) @definition.type
(enum_specifier name: (type_identifier) @name) @definition.type
(type_definition declarator: (type_identifier) @name) @definition.type
(alias_declaration name: (type_identifier) @name) @definition.type
(enumerator name: (identifier) @name) @definition.constant

; Callables and state. A `function_definition` has a body by construction and is
; the one unambiguous definition shape; `field_declaration` is a class member,
; which is a definition wherever it appears because the class body *is* the
; definition of what the class has; `declaration` is everything else and cc.go
; decides.
;
; `template_declaration` is deliberately not captured. It is transparent: it
; wraps the class or function it parameterises, contributes no descriptor
; component of its own, and its template parameters are not emitted. cc.go says
; what that collides.
(function_definition) @definition.function
(field_declaration) @definition.field
(parameter_declaration) @definition.parameter
(optional_parameter_declaration) @definition.parameter
(declaration) @declaration

(preproc_def name: (identifier) @name) @definition.constant
(preproc_function_def name: (identifier) @name) @definition.function

; --------------------------------------------------------------- imports -----
;
; Two mechanisms, and only one of them is C++'s own.
;
; `#include` is C's, unchanged and still textual; cc.go approximates the build
; system's `-I` search by path suffix.
;
; `using` is the one that imports a *name*: `using namespace greeter;` names a
; namespace and `using greeter::Greeter;` names a symbol in one. The two are the
; same node kind, told apart by whether the child is a bare identifier or a
; qualified one, so cc.go asks.
(preproc_include path: (string_literal) @name) @import
(preproc_include path: (system_lib_string) @name) @import
(using_declaration) @import
(namespace_alias_definition) @import

; ------------------------------------------------------------ references -----
;
; As narrow as C's, with C++'s two extra shapes.
;
; `(qualified_identifier)` is where this dialect earns its complexity and where
; it inherits Ruby's unanswerable question. `A::B` says nothing about whether
; `A` is a namespace or a class, and the grammar labels the scope
; `namespace_identifier` either way. cc.go's rule — the innermost qualifier of a
; declarator is a type, everything before it is a namespace — is argued in its
; package comment; it is the rule that makes `std::string Greeter::greet()` in
; the source join the `std::string greet();` in the header, which is the whole
; point of the dialect.
(call_expression function: (identifier) @name) @reference.call
(call_expression function: (qualified_identifier) @name) @reference.call
(call_expression function: (field_expression field: (field_identifier) @name)) @reference.call
(field_expression field: (field_identifier) @name) @reference.read

(qualified_identifier) @reference.scoped
(type_identifier) @reference.type
(base_class_clause (type_identifier) @name) @reference.type
