; Swift stanza — tree-sitter query half (SPEC.md §5).
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
; This file says *where* things are. swift.go says what they are called: it
; builds the SCIP descriptor suffix by walking each captured node's ancestors,
; assigns role and symbol_kind, and resolves same-file references. Anything that
; needs another file is emitted unresolved (§4.3).
;
; Two properties of this grammar shape the patterns below, and both are worth
; stating once rather than at every site.
;
; **It names no fields at all.** Kotlin's grammar defined two; this one defines
; none, so a name is identified by its node type and its position among its
; siblings. `(function_declaration (simple_identifier) @name)` works because a
; function declaration has exactly one direct `simple_identifier` child and it is
; the name — the parameters are wrapped in `parameter` and the return type in
; `user_type`, so neither is a direct child.
;
; **It spells `struct`, `class`, `enum`, `actor` and `extension` with one node
; type.** `class_declaration` is all five, with the keyword kept as an anonymous
; child. What tells an extension from the rest is what it holds a name in: a
; declaration writes a `type_identifier` (the name it declares), an extension
; writes a `user_type` (the type it extends, which it does not declare). That
; single difference is what makes the definition patterns below correct without
; a predicate — and it is why an extension defines nothing here. See swift.go.

; ---------------------------------------------------------------- scopes -----
;
; Swift's lexical scopes are the C-family ones, as Kotlin's and Java's are.
; `@scope.package` is absent for Kotlin's reason and more so: Swift writes no
; namespace declaration anywhere, so the file scope *is* the module scope.
;
; The *declaration* is captured rather than the body, as every stanza since Java
; does: a function's parameters are written outside its body and have to land
; inside its scope, and a type's own name has to land inside the type.
;
; `guard_statement` is deliberately **not** a scope, and it is the one place this
; list departs from Kotlin's. `guard let x = f() else { return }` binds `x` for
; the *rest of the enclosing scope* — that is the whole point of the statement —
; so a scope around it would hide the binding from every line that uses it. The
; `else` branch's own bindings leak one scope outward as a result, which costs a
; name resolving one level too widely and never a name resolving to the wrong
; thing.
;
; A getter, a setter, a `willSet`/`didSet` observer and a closure are function
; scopes: each is a body with bindings of its own that outlive nothing.

(source_file) @scope.file
(class_declaration) @scope.type
(protocol_declaration) @scope.type
(function_declaration) @scope.function
(protocol_function_declaration) @scope.function
(init_declaration) @scope.function
(deinit_declaration) @scope.function
(subscript_declaration) @scope.function
(computed_property) @scope.function
(computed_getter) @scope.function
(computed_setter) @scope.function
(willset_didset_block) @scope.function
(lambda_literal) @scope.function
(if_statement) @scope.block
(for_statement) @scope.block
(while_statement) @scope.block
(repeat_while_statement) @scope.block
(do_statement) @scope.block
(catch_block) @scope.block
(switch_entry) @scope.block

; ----------------------------------------------------------- definitions -----
;
; The file itself is the definition of a module, as it is in every language here
; — but Swift is the first that writes *nothing at all* to say which. Go, Java,
; Kotlin, C# and PHP declare a namespace in the source; TypeScript, Python and
; Rust derive one from the file's own position; Swift's namespace is the SwiftPM
; **target**, which is declared in `Package.swift` and nowhere else, and §2.5
; forbids the extractor from reading another file. swift.go derives it from the
; path convention SwiftPM itself enforces, and states the cost. There is nothing
; in the CST to capture, so the source_file is the whole pattern.
;
; It carries the neutral-core `package` kind because that is the kind link's
; `imports` derivation joins on.

(source_file) @definition.package

; The type-declaring forms. `class_declaration` is `struct`, `class`, `enum` and
; `actor` — and `extension`, which is why the pattern insists on a direct
; `type_identifier`: an extension writes a `user_type` instead, so it does not
; match here and defines nothing. That is deliberate and is argued in swift.go —
; an extension adds members to a type it does not declare, and claiming it as a
; definition would put `String` in the `definedIn` of a file that only extends it.
;
; A protocol is captured apart because link keys `implements` off the `interface`
; kind, which swift.go assigns from this node type alone.
(class_declaration (type_identifier) @name) @definition.type
(protocol_declaration (type_identifier) @name) @definition.type

; A type alias is a name for a type, which is what the neutral core's `type` kind
; means. An `associatedtype` is a protocol's type requirement, which is the same
; reading — it is a name that stands for a type, resolved by the conformer.
(typealias_declaration (type_identifier) @name) @definition.type
(associatedtype_declaration (type_identifier) @name) @definition.type

; Functions. Swift has free functions and members and one node type for both, so
; this captures `function` and swift.go promotes the ones written inside a type
; to `method` — Kotlin's arrangement exactly. A protocol's requirement is a
; second node type and is a member by construction.
;
; `init`, `deinit` and `subscript` are captured nowhere: none of them writes an
; identifier, so there is no component to build a descriptor from. An initialiser
; needs none — `Greeter(name:)` is written as the *type's* own name, so swift.go
; resolves a construction to the type's descriptor, which is a definition that
; exists. A `deinit` is unreferenceable by construction. A `subscript` is
; referenced as `a[i]`, which names nothing, so a definition for it could never
; be joined; it is a scope here and not a symbol.
(function_declaration (simple_identifier) @name) @definition.function
(protocol_function_declaration (simple_identifier) @name) @definition.function

; Properties. The binding is wrapped in a `pattern`, and the pattern nests for
; the destructuring form — `let (a, b) = pair` is a pattern of two patterns —
; which is why there are two spellings rather than one.
;
; `variable` is the capture and swift.go refines it: a property declared in a
; type body is a field, a top-level `let` is a constant, and a local stays a
; variable.
(property_declaration (pattern (simple_identifier) @name)) @definition.variable
(property_declaration (pattern (pattern (simple_identifier) @name))) @definition.variable
(for_statement (pattern (simple_identifier) @name)) @definition.variable

; The optional bindings, which are three statements and one recovery shape with
; one thing in common: a `value_binding_pattern` immediately followed by the name
; it binds, with no `pattern` node between them.
;
;   guard let last = xs.last else { … }   — a guard binding
;   catch let error { … }                 — a caught error
;   if let first = xs.first { … }         — **and this one does not parse**
;
; The pinned grammar (gotreesitter v0.47.1) cannot parse an `if` whose condition
; is an optional binding: `if let x = y { … }` yields an ERROR node, while
; `guard let` and `while let` parse. That is a property of the grammar and not
; something this stanza can fix without changing go.mod, which §14 M9+ forbids —
; so the recovery shape is read rather than fought. Tree-sitter's ERROR node
; keeps its children, and the children it keeps are the same three the guard form
; writes, so the last pattern below extracts the binding that the parse lost.
;
; Anchored (`.`) in all four, because an ERROR node holds everything the parser
; could not place: without the anchor the pattern would claim every identifier in
; the failed region as a binding.
(guard_statement (value_binding_pattern) . (simple_identifier) @name) @definition.variable
(while_statement (value_binding_pattern) . (simple_identifier) @name) @definition.variable
(pattern (value_binding_pattern) . (simple_identifier) @name) @definition.variable
(ERROR (value_binding_pattern) . (simple_identifier) @name) @definition.variable

; An enum case. Whether it carries associated values decides how it is written at
; every use site and therefore what its descriptor has to be — see swift.go's
; caseKind; the capture is one because the declaration is one node type.
(enum_entry (simple_identifier) @name) @definition.field

; Parameters, in the three places Swift writes one. A `parameter` may hold two
; identifiers rather than one — `func move(to point: Point)` writes an external
; label and an internal name — and only the second is a binding; swift.go takes
; the last and claims the label so nothing emits it as a read.
(parameter (simple_identifier) @name) @definition.parameter
(lambda_parameter (simple_identifier) @name) @definition.parameter
(type_parameter (type_identifier) @name) @definition.parameter

; --------------------------------------------------------------- imports -----
;
; One statement, and the thing to know about it is that Swift's import is a
; **whole-module wildcard**. `import Greeter` brings every public declaration of
; the module into scope under its simple name, and there is no per-name form of
; it except the declaration import:
;
;   * `import Greeter`                — the module, on demand.
;   * `import Foundation.NSString`    — a submodule of a C module.
;   * `import struct Greeter.Greeter` — one declaration, and the only spelling
;                                       that binds a nameable thing.
;
; The kind keyword (`struct`, `class`, `enum`, `protocol`, `typealias`, `func`,
; `var`, `let`) is an anonymous child, so one pattern covers all three and
; swift.go asks the statement which it is. What the wildcard costs is the central
; limit of this stanza and is argued in its package comment.

(import_declaration (identifier) @name) @import

; ------------------------------------------------------------ references -----
;
; A call and a plain read can capture the same identifier — `g.greet()` yields a
; @reference.call on `greet` and, on `g`, a @reference.read. swift.go dedupes by
; identifier range, preferring the more specific role, and drops any reference
; landing on a node a definition or an import already claimed.
;
; Only reads. `@reference.write` has no landing site in the core model: the
; occurrence table records role `definition` or `reference` and nothing else, so
; an assignment's left-hand side is a reference like any other.

; A call is either bare or reached through a navigation. `?.` needs no pattern of
; its own: the optional-chaining operator lives inside `navigation_suffix` beside
; the member name, so `g?.greet()` and `g.greet()` are the same shape and the
; member is the same node.
(call_expression (simple_identifier) @name) @reference.call
(call_expression (navigation_expression (navigation_suffix (simple_identifier) @name))) @reference.call

; The two halves of `a.b`: the receiver and the member. `a.b = x` writes the same
; two halves under a different node type, because the grammar marks an
; assignment's target as it parses it — and the target of a write is a reference
; like any other.
;
; The receiver of a navigation is **not** captured here, and that is Swift's own
; wrinkle rather than an omission. This grammar parses a bare identifier in
; receiver position as a `user_type`: `g.greet()` puts `g` under a `user_type`
; node whatever `g` is, so the receiver arrives through the `(user_type)` pattern
; at the bottom of this file and swift.go demotes it from a type reference to a
; read when it sees where it sits. Capturing it here as well would produce two
; candidates for one identifier and make the dedupe decide a question the CST
; already answers.
(directly_assignable_expression (simple_identifier) @name) @reference.read
(navigation_suffix (simple_identifier) @name) @reference.read

; Every other identifier used as a value — an argument, a return expression, an
; operand, the right-hand side of an assignment. Kotlin's and Ruby's stanzas
; capture the bare identifier for the same reason: Swift writes a plain read as
; nothing but the name, so a narrower pattern list would resolve `return name` to
; nothing. What it over-captures is dropped rather than emitted — a definition's
; own name is claimed, an argument label is recognised and skipped, and a
; navigation's halves win the dedupe because their node is wider and knows more.
(simple_identifier) @reference.read

; A string interpolation's `\(name)`. It is a read of a binding in scope written
; where no other stanza's patterns reach, because the grammar wraps it in a node
; of its own.
(interpolated_expression (simple_identifier) @name) @reference.read

; Types, in every position at once: a declared type, a return type, a type
; argument, an attribute, the `:` conformance list — which is where Swift says a
; type adopts a protocol — and the `extension` header, which is how the members
; below it find the type they belong to.
;
; The `user_type` is captured rather than its `type_identifier`, because a
; qualified type is a *flat* list of `type_identifier` children under one
; `user_type` (`Greeter.Greeter` is two of them) and capturing the leaf would
; emit `Greeter` twice as two unrelated types. swift.go reads the whole path off
; the node and hangs the occurrence on its last segment.
(user_type) @reference.type
