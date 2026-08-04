; Kotlin stanza — tree-sitter query half (SPEC.md §5).
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
; This file says *where* things are. kotlin.go says what they are called: it
; builds the SCIP descriptor suffix by walking each captured node's ancestors,
; assigns role and symbol_kind, and resolves same-file references. Anything that
; needs another file is emitted unresolved (§4.3).
;
; One property of this grammar shapes every pattern below, and it is worth
; stating once rather than nine times: **it names almost no fields**. Java's
; stanza writes `(method_declaration name: (identifier) @name)`; the Kotlin
; grammar defines two field names in the whole language (`receiver` and
; `condition`), so a name is identified by its node type and its position among
; its siblings instead. `(function_declaration (simple_identifier) @name)` works
; because a function declaration has exactly one direct `simple_identifier` child
; and it is the name — the parameters are wrapped in `function_value_parameters`
; and the return type in `user_type`, so neither is a direct child of this type.
; Where position alone is not enough, kotlin.go looks at the node.

; ---------------------------------------------------------------- scopes -----
;
; Kotlin's lexical scopes are the C-family ones, as Java's and Rust's are: a
; block really does scope its declarations. `@scope.package` is absent for Java's
; reason — a `package` clause is declared once at the top of a file and is never
; a lexical region below it, so the file scope *is* the package scope.
;
; The *declaration* is captured rather than the body, as Java and Rust do: a
; function's parameters are written outside its body and have to land inside its
; scope, and a type's own name has to land inside the type.
;
; `primary_constructor` is deliberately **not** a scope, and that is the one
; place this list departs from Java's. `class Greeter(private val prefix: String)`
; declares a property of the class, not an argument of a function: it is in scope
; in every member below it, so a scope around it would hide it from all of them.
; A `secondary_constructor` is a real function scope and is one.
;
; A getter, a setter, a lambda and an `init` block are function scopes: each is a
; body with bindings of its own that outlive nothing.

(source_file) @scope.file
(class_declaration) @scope.type
(object_declaration) @scope.type
(companion_object) @scope.type
(function_declaration) @scope.function
(secondary_constructor) @scope.function
(anonymous_initializer) @scope.function
(getter) @scope.function
(setter) @scope.function
(lambda_literal) @scope.function
(control_structure_body) @scope.block
(for_statement) @scope.block
(while_statement) @scope.block
(catch_block) @scope.block
(when_entry) @scope.block

; ----------------------------------------------------------- definitions -----
;
; The file itself is the definition of a package, as it is in every language
; here. Kotlin writes the name down, as Go and Java do and unlike TypeScript,
; Python and Rust: `package com.example.greeter` at the top of the file. Both
; patterns are captured and kotlin.go prefers the declaration; the second is what
; a file with no package clause falls back to, and there is nothing else it could
; use, since §2.5 forbids asking the filesystem.
;
; It carries the neutral-core `package` kind because that is the kind link's
; `imports` derivation joins on.

(source_file) @definition.package
(package_header (identifier) @name) @definition.package

; The type-declaring forms, and there are three node types for what Java spells
; with five. `class_declaration` covers `class`, `interface`, `enum class` and
; `annotation class` alike — the grammar keeps the keyword as an anonymous child
; rather than as a node type — so the interface case is refined in kotlin.go
; instead of being captured apart. link keys `implements` off that kind.
;
; An `object` is a singleton and is a type: it declares members that are reached
; through its name. A named `companion object` is one too; an unnamed one has no
; identifier to hang an occurrence on and so defines nothing. Either way its
; *members* are descriptored on the enclosing class rather than under it, because
; that is how they are written at every use site — see kotlin.go's
; containerSuffix.
(class_declaration (type_identifier) @name) @definition.type
(object_declaration (type_identifier) @name) @definition.type
(companion_object (type_identifier) @name) @definition.type

; A type alias is a name for a type, which is what the neutral core's `type` kind
; means. Kotlin's is not a new type and the distinction is not one the core model
; asks about.
(type_alias (type_identifier) @name) @definition.type

; Functions. Kotlin has free functions and members, and one node type for both —
; the difference is what the declaration sits inside — so this captures
; `function` and kotlin.go promotes the ones inside a type to `method`. That is
; the opposite of Java, which has no free function at all.
;
; A constructor is captured nowhere. Kotlin writes neither a primary nor a
; secondary constructor with a name, so there is no identifier to build a
; component from — and none is needed: `Greeter("x")` is written as the type's
; own name, so kotlin.go resolves a constructor call to the *type's* descriptor,
; which is a definition that exists.
(function_declaration (simple_identifier) @name) @definition.function

; Properties, in the two shapes a declaration can take. `val (a, b) = pair`
; declares two of them, which is why the destructuring form is captured through
; `multi_variable_declaration` rather than beside it.
;
; `variable` is the capture and kotlin.go refines it: a `const val` is a
; constant, a property declared in a type body is a field, and a local stays a
; variable.
(property_declaration (variable_declaration (simple_identifier) @name)) @definition.variable
(property_declaration (multi_variable_declaration (variable_declaration (simple_identifier) @name))) @definition.variable

; Local bindings that are not property declarations: a `for` variable, in both
; the plain and the destructuring spelling.
(for_statement (variable_declaration (simple_identifier) @name)) @definition.variable
(for_statement (multi_variable_declaration (variable_declaration (simple_identifier) @name))) @definition.variable

; An enum entry is a constant of its enum type, which is what `field` means here
; — the same reading java.go gives `enum_constant`.
(enum_entry (simple_identifier) @name) @definition.field

; Parameters, in the five places Kotlin writes one. A `class_parameter` is the
; odd one: `class Person(val name: String)` writes a property where a parameter
; is written, and kotlin.go promotes the ones carrying `val`/`var` to fields —
; which is what they are, and the same promotion java.go makes for a record's
; component.
(parameter (simple_identifier) @name) @definition.parameter
(class_parameter (simple_identifier) @name) @definition.parameter
(catch_block (simple_identifier) @name) @definition.parameter
(lambda_parameters (variable_declaration (simple_identifier) @name)) @definition.parameter
(setter (parameter_with_optional_type (simple_identifier) @name)) @definition.parameter
(type_parameter (type_identifier) @name) @definition.parameter

; --------------------------------------------------------------- imports -----
;
; One statement, three spellings, and @name is the dotted `identifier` node
; kotlin.go reads the path off in every one of them:
;
;   * `import com.example.Greeter`      — a type.
;   * `import com.example.*`            — a package, on demand.
;   * `import kotlin.math.max as maxOf` — one declaration, renamed.
;
; Kotlin imports *declarations*, not only types, and that is the difference from
; Java that kotlin.go's path splitting exists for: `kotlin.math.max` names a
; top-level function, so the last segment is a member of a package rather than a
; deeper package. The `wildcard_import` and the `import_alias` are siblings of
; the path rather than part of it, so one pattern covers all three and kotlin.go
; asks the statement which it is.

(import_header (identifier) @name) @import

; ------------------------------------------------------------ references -----
;
; A call and a plain read can capture the same identifier — `g.greet()` yields a
; @reference.call on `greet` and, on `g`, a @reference.read. kotlin.go dedupes by
; identifier range, preferring the more specific role, and drops any reference
; landing on a node a definition or an import already claimed.
;
; Only reads. `@reference.write` has no landing site in the core model: the
; occurrence table records role `definition` or `reference` and nothing else, so
; an assignment's left-hand side is a reference like any other.

; A call is either bare or reached through a navigation. `?.` needs no pattern of
; its own: the safe-call operator lives inside `navigation_suffix` beside the
; member name, so `g?.greet()` and `g.greet()` are the same shape and the member
; is the same node. (The gotreesitter grammar notes a display-name disagreement
; on `"\?."`; it is a property of the anonymous token's *name*, and nothing here
; names it.)
(call_expression (simple_identifier) @name) @reference.call
(call_expression (navigation_expression (navigation_suffix (simple_identifier) @name))) @reference.call

; The two halves of `a.b`: the receiver and the member. `a.b = x` writes the
; same two halves under a different node type, because the grammar marks an
; assignment's target as it parses it — and the target of a write is a reference
; like any other, since the occurrence table records `definition` or `reference`
; and has no third role for it.
(navigation_expression (simple_identifier) @name) @reference.read
(directly_assignable_expression (simple_identifier) @name) @reference.read
(navigation_suffix (simple_identifier) @name) @reference.read

; Every other identifier used as a value — an argument, a return expression, an
; operand, the right-hand side of an assignment. Ruby's stanza captures the bare
; identifier for the same reason: Kotlin writes a plain read as nothing but the
; name, so a narrower pattern list would resolve `return name` to nothing. What
; it over-captures is dropped rather than emitted — a definition's own name is
; claimed, and a navigation's halves win the dedupe because their node is wider
; and knows more.
(simple_identifier) @reference.read

; A string template's `$name`. It is a read of a binding in scope written in a
; place no other language here can write one, and the grammar gives it its own
; node type rather than reusing `simple_identifier`.
(interpolated_identifier) @reference.read

; Types, in every position at once: a declared type, a return type, a type
; argument, an annotation, and — the ones that matter most — the `:` supertype
; list, which is where Kotlin declares that a class implements an interface.
;
; The `user_type` is captured rather than its `type_identifier`, because a
; qualified type is a *flat* list of `type_identifier` children under one
; `user_type` (`com.example.util.Formatter` is four of them) and capturing the
; leaf would emit `com`, `example` and `util` as three types that do not exist.
; kotlin.go reads the whole path off the node and hangs the occurrence on its
; last segment.
(user_type) @reference.type
