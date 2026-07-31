; Java stanza — tree-sitter query half (SPEC.md §5).
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
; This file says *where* things are. java.go says what they are called: it builds
; the SCIP descriptor suffix by walking each captured node's ancestors, assigns
; role and symbol_kind, and resolves same-file references. Anything that needs
; another file is emitted unresolved (§4.3).

; ---------------------------------------------------------------- scopes -----
;
; Java's lexical scopes are the C-family ones, as Rust's are and unlike Python's:
; a `block` really does scope its declarations.
;
; The *declaration* is captured rather than the body, for the reason the Rust
; stanza captures `(function_item)` and not its block: a method's parameters are
; written outside its body and have to land inside its scope, and a type's own
; name has to land inside the type. `@scope.package` is absent, and that is a
; real difference from Rust — a Java package is declared once at the top of a
; file and is never a lexical region below it, so the file scope *is* the package
; scope exactly as it is in Go, TypeScript and Python.
;
; A lambda and a static initialiser are both function scopes: each is a body with
; bindings of its own that outlive nothing.

(program) @scope.file
(class_declaration) @scope.type
(interface_declaration) @scope.type
(enum_declaration) @scope.type
(record_declaration) @scope.type
(annotation_type_declaration) @scope.type
(method_declaration) @scope.function
(constructor_declaration) @scope.function
(compact_constructor_declaration) @scope.function
(lambda_expression) @scope.function
(static_initializer) @scope.function
(block) @scope.block
(for_statement) @scope.block
(enhanced_for_statement) @scope.block
(catch_clause) @scope.block

; ----------------------------------------------------------- definitions -----
;
; The file itself is the definition of a package, as it is in every language
; here. Java is the first since Go to *write the name down*: TypeScript, Python
; and Rust have no clause naming the module a file is, so all three name it from
; the path, while Java states `package com.example.greeter;` outright. Both
; patterns are captured and java.go prefers the declaration — the second is what
; a file in the default package falls back to, and there is nothing else it could
; use, since §2.5 forbids asking the filesystem.
;
; It carries the neutral-core `package` kind because that is the kind link's
; `imports` derivation joins on. Not `module`: the word is the natural one and
; the kind is the working one.

(program) @definition.package
(package_declaration (_) @name) @definition.package

; The type-declaring forms. An interface and an annotation type are captured as
; `interface` directly rather than refined afterwards — the CST already says
; which they are, and link keys `implements` off that kind — while a class, an
; enum and a record are data and are captured as `type`. This is the same split
; TypeScript draws and the opposite of the work Python's stanza has to do, where
; a Protocol is a class until its bases are inspected.
(class_declaration name: (identifier) @name) @definition.type
(enum_declaration name: (identifier) @name) @definition.type
(record_declaration name: (identifier) @name) @definition.type
(interface_declaration name: (identifier) @name) @definition.interface
(annotation_type_declaration name: (identifier) @name) @definition.interface

; Methods and constructors. Every Java callable is a member of a type — the
; language has no free function — so there is no `@definition.function` in this
; file at all, which is the one place Java's vocabulary is narrower than every
; earlier language's rather than wider.
(method_declaration name: (identifier) @name) @definition.method
(constructor_declaration name: (identifier) @name) @definition.method
(compact_constructor_declaration name: (identifier) @name) @definition.method

; Fields, enum constants and annotation elements. A field's name lives on the
; `variable_declarator`, not on the declaration, because one `int x, y;` declares
; two of them; java.go reads the declaration's `type` field through the parent.
(field_declaration declarator: (variable_declarator name: (identifier) @name)) @definition.field
(enum_constant name: (identifier) @name) @definition.field
(annotation_type_element_declaration name: (identifier) @name) @definition.field

(constant_declaration declarator: (variable_declarator name: (identifier) @name)) @definition.constant

; Local bindings, in the three places Java writes one.
(local_variable_declaration declarator: (variable_declarator name: (identifier) @name)) @definition.variable
(enhanced_for_statement name: (identifier) @name) @definition.variable
(resource name: (identifier) @name) @definition.variable

; Parameters, of a method, a constructor, a catch clause and a lambda. A record's
; component is a `formal_parameter` too and java.go promotes it to a field, which
; is what it is: `record Point(int x, int y)` declares state and an accessor for
; it, not an argument.
(formal_parameter name: (identifier) @name) @definition.parameter
(spread_parameter (variable_declarator name: (identifier) @name)) @definition.parameter
(catch_formal_parameter name: (identifier) @name) @definition.parameter
(inferred_parameters (identifier) @name) @definition.parameter
(type_parameter (type_identifier) @name) @definition.parameter

; --------------------------------------------------------------- imports -----
;
; One statement, four spellings, and @name is the dotted node java.go reads the
; path off in every one of them:
;
;   * `import a.b.C;`               — a single type.
;   * `import a.b.*;`               — a package, on demand.
;   * `import static a.b.C.m;`      — one static member of a type.
;   * `import static a.b.C.*;`      — a type's static members, on demand.
;
; Nothing in the syntax separates the package part of the path from the type
; part: `a.b.C` and `a.b.c` are the same shape, and `import a.b.C.D;` names a
; nested type while `import a.b.c.D;` names a top-level one in a deeper package.
; java.go splits them on Java's naming convention — lowercase segments are the
; package, the first uppercase one begins the type — which is the trade Rust's
; stanza makes for `use a::b;` versus `use a::B;` and for the same reason: the
; convention is universal, and being wrong yields a descriptor that matches
; nothing rather than one that matches the wrong thing.
;
; The `asterisk` is a sibling of the path rather than part of it, so the same
; pattern covers the on-demand forms and java.go asks the statement whether one
; is there.

(import_declaration (_) @name) @import

; ------------------------------------------------------------ references -----
;
; A call and a plain read can capture the same identifier — `g.greet()` yields
; both a @reference.call on `greet` and, on `g`, a @reference.read. java.go
; dedupes by identifier range, preferring the more specific role, and drops any
; reference landing on a node a definition or an import already claimed (which is
; how the bare `(type_identifier)` pattern below avoids re-emitting
; `class Greeter`).
;
; Java has one member operator and it is overloaded three ways: `a.b` is a field
; of a value, a member of a type, or a segment of a package name, and the syntax
; is identical in all three. java.go decides by what the left half resolves to —
; a binding in scope, an imported name, or a package path — which is the same
; question Rust answers with two operators instead of one.
;
; Only reads. `@reference.write` has no landing site in the core model: the
; occurrence table records role `definition` or `reference` and nothing else, so
; an assignment's left-hand side is a reference like any other.

(method_invocation name: (identifier) @name) @reference.call

(method_invocation object: (identifier) @name) @reference.read
(field_access object: (identifier) @name) @reference.read
(field_access field: (identifier) @name) @reference.read
(scoped_identifier scope: (identifier) @name) @reference.read
(scoped_identifier name: (identifier) @name) @reference.read

; Types, in every position at once: an annotation, a declared type, a return
; type, a cast, a type argument, a `new`, and — the two that matter most here —
; the `extends` and `implements` clauses. Java declares its interface
; satisfaction explicitly, and this is where the declaration is recorded: as what
; it structurally is, a reference to the interface's type, which needs no edge
; kind the schema does not already have.
(type_identifier) @reference.type
(scoped_type_identifier) @reference.type
