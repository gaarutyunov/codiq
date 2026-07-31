; C# stanza — tree-sitter query half (SPEC.md §5).
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
; This file says *where* things are. cs.go says what they are called: it builds
; the SCIP descriptor suffix by walking each captured node's ancestors, assigns
; role and symbol_kind, and resolves same-file references. Anything that needs
; another file is emitted unresolved (§4.3).
;
; One thing about this grammar shapes the whole file, and it is the one real
; difference from Java's query. C# has no `type_identifier` node: a type is
; written with the same `identifier`, `qualified_name` and `generic_name` nodes an
; expression is, so `@reference.type` cannot be a pattern over a node *kind* and
; has to be a pattern over each *position* a type may appear in. Each of those
; patterns captures the type expression whole — `(_)`, not a specific node — and
; cs.go reduces it to the identifier the occurrence covers, which is the same
; unwrapping `unwrapType` already had to do for `List<string>` and `Foo[]`.

; ---------------------------------------------------------------- scopes -----
;
; C#'s lexical scopes are the C-family ones, as Java's and Rust's are.
;
; The *declaration* is captured rather than the body, for the reason the Java
; stanza gives: a method's parameters are written outside its body and have to
; land inside its scope, and a type's own name has to land inside the type.
;
; `@scope.package` is here, and it is the one scope kind Java's query does not
; have. A Java package is declared once at the top of a file and is never a
; lexical region below it; a C# *block* namespace genuinely is one — it has
; braces, it nests, and a file may hold several side by side — so it is a scope
; in the way `namespace Foo;` is not. The file-scoped form is captured as a
; definition below and not as a scope, because it delimits nothing: everything
; after it is a sibling, not a child.
;
; A lambda, an anonymous method, a local function and a property accessor are all
; function scopes: each is a body with bindings of its own that outlive nothing.

(compilation_unit) @scope.file
(namespace_declaration) @scope.package
(class_declaration) @scope.type
(struct_declaration) @scope.type
(record_declaration) @scope.type
(interface_declaration) @scope.type
(enum_declaration) @scope.type
(method_declaration) @scope.function
(constructor_declaration) @scope.function
(destructor_declaration) @scope.function
(operator_declaration) @scope.function
(conversion_operator_declaration) @scope.function
(indexer_declaration) @scope.function
(local_function_statement) @scope.function
(accessor_declaration) @scope.function
(lambda_expression) @scope.function
(anonymous_method_expression) @scope.function
(block) @scope.block
(for_statement) @scope.block
(foreach_statement) @scope.block
(catch_clause) @scope.block
(switch_section) @scope.block

; ----------------------------------------------------------- definitions -----
;
; The file itself is the definition of a package, as it is in every language
; here — but C# is the first language in this graph where "the file's namespace"
; is not a single answer, so all three shapes are captured and cs.go decides.
;
;   * `namespace Foo.Bar;`        — file-scoped (C# 10+). One namespace, and
;                                   everything after it is a sibling of it.
;   * `namespace Foo.Bar { … }`   — a block. It nests, and a file may hold
;                                   several of them side by side, so the
;                                   namespace is a property of a *declaration*
;                                   and not of the file.
;   * `(compilation_unit)`        — neither, which is the global namespace.
;
; The kind is the neutral core's `package` because that is the kind link's
; `imports` derivation joins on. Not `module`: the word is the natural one for a
; language whose compilation unit is an assembly, and the kind is the working one.

(compilation_unit) @definition.package
(file_scoped_namespace_declaration name: (_) @name) @definition.package
(namespace_declaration name: (_) @name) @definition.package

; The type-declaring forms. An interface is captured as `interface` directly
; rather than refined afterwards — the CST already says which it is, and link
; keys `implements` off that kind — while a class, a struct, a record, an enum
; and a delegate are data and are captured as `type`. This is the same split Java
; and TypeScript draw.
(class_declaration name: (identifier) @name) @definition.type
(struct_declaration name: (identifier) @name) @definition.type
(record_declaration name: (identifier) @name) @definition.type
(enum_declaration name: (identifier) @name) @definition.type
(delegate_declaration name: (identifier) @name) @definition.type
(interface_declaration name: (identifier) @name) @definition.interface

; Callables. A method and a constructor are members of a type; a local function
; is not, which is why it is a `function` and they are `method`s — and its
; descriptor carries the enclosing method's `().` component, which falls out of
; the ancestor walk with no special case.
;
; An operator, a conversion operator, an indexer and a destructor are absent on
; purpose. None of the four has an identifier a *reference* could reconstruct:
; `a + b`, `x[i]`, an implicit conversion and a finalizer call are all written
; without naming the member they reach, so a definition-side descriptor for them
; would be one only the definition side can compute — the same reason java.go
; gives for not numbering overloads.
(method_declaration name: (identifier) @name) @definition.method
(constructor_declaration name: (identifier) @name) @definition.method
(local_function_statement name: (identifier) @name) @definition.function

; State. A property and an event are captured as fields, which is what the
; neutral core's `field` kind means: they are state a type holds, and the
; accessors C# generates around them are not what a caller names. A record's
; positional component is a `parameter` in this grammar and cs.go promotes it to
; a field for the same reason java.go does.
(field_declaration (variable_declaration (variable_declarator name: (identifier) @name))) @definition.field
(event_field_declaration (variable_declaration (variable_declarator name: (identifier) @name))) @definition.field
(property_declaration name: (identifier) @name) @definition.field
(event_declaration name: (identifier) @name) @definition.field
(enum_member_declaration name: (identifier) @name) @definition.field

; Local bindings, in the three places C# writes one. A `const` field is a field
; here and cs.go promotes it to a constant — C# has no distinct node for one, so
; the modifier is the only thing that says so.
(local_declaration_statement (variable_declaration (variable_declarator name: (identifier) @name))) @definition.variable
(foreach_statement left: (identifier) @name) @definition.variable
(using_statement (variable_declaration (variable_declarator name: (identifier) @name))) @definition.variable

; Parameters, of a callable, a lambda, a catch clause and a generic declaration.
(parameter name: (identifier) @name) @definition.parameter
(catch_declaration name: (identifier) @name) @definition.parameter
(type_parameter name: (identifier) @name) @definition.parameter

; --------------------------------------------------------------- imports -----
;
; One statement, four spellings, and cs.go reads the path off the last named
; child in every one of them:
;
;   * `using System.Text;`          — a namespace, on demand.
;   * `global using System.Text;`   — the same, for every file in the assembly.
;   * `using static System.Console;` — one type's static members, on demand.
;   * `using B = System.Text.Builder;` — an alias for one type.
;
; The directive is captured whole rather than its path, because the alias form
; has two named children and a pattern capturing `(_)` would match twice.
;
; Unlike Java's, the plain form needs no convention to read: `using a.b.c;`
; names a namespace and can name nothing else, so the whole path is the
; namespace and there is no package/type split to guess at. That matters because
; this is the occurrence link's `imports` derivation joins against, and it is the
; only place in this stanza where the split has to be exactly right.

(using_directive) @import

; ------------------------------------------------------------ references -----
;
; A call and a plain read can capture the same identifier — `g.Greet()` yields
; both a @reference.call on `Greet` and, on `g`, a @reference.read. cs.go dedupes
; by identifier range, preferring the more specific role, and drops any reference
; landing on a node a definition or a using directive already claimed.
;
; C# writes member access two ways and they are the same question: `.` in an
; expression is a `member_access_expression`, `.` in a type or a using directive
; is a `qualified_name`, and both mean "a member of whatever the left half is".
; cs.go decides what the left half is by what it resolves to — a binding in
; scope, an alias, a namespace this file has been told about, or a name that is
; none of those.
;
; Only reads. `@reference.write` has no landing site in the core model: the
; occurrence table records role `definition` or `reference` and nothing else, so
; an assignment's left-hand side is a reference like any other.

(invocation_expression function: (identifier) @name) @reference.call
(invocation_expression function: (member_access_expression name: (identifier) @name)) @reference.call

(member_access_expression expression: (identifier) @name) @reference.read
(member_access_expression name: (identifier) @name) @reference.read
(qualified_name qualifier: (identifier) @name) @reference.read
(qualified_name name: (identifier) @name) @reference.read

; Types, in every position the grammar puts one — which is the shape this query
; has and Java's does not, because C# has no node kind that means "a type name".
; The captured node is the type expression whole and cs.go reduces it: `Foo[]`,
; `Foo?`, `List<Foo>` and `A.B.Foo` all name `Foo`, and `int`, `string` and `var`
; name nothing at all.
;
; `base_list` is the one that matters most here. C# declares interface
; satisfaction explicitly, and this is where the declaration is recorded: as what
; it structurally is, a reference to the interface's type, which needs no edge
; kind the schema does not already have.
(base_list (_) @name) @reference.type
(variable_declaration type: (_) @name) @reference.type
(parameter type: (_) @name) @reference.type
(method_declaration returns: (_) @name) @reference.type
(local_function_statement type: (_) @name) @reference.type
(property_declaration type: (_) @name) @reference.type
(indexer_declaration type: (_) @name) @reference.type
(delegate_declaration returns: (_) @name) @reference.type
(catch_declaration type: (_) @name) @reference.type
(foreach_statement type: (_) @name) @reference.type
(object_creation_expression type: (_) @name) @reference.type
(cast_expression type: (_) @name) @reference.type
(type_argument_list (_) @name) @reference.type
(array_type type: (_) @name) @reference.type
(nullable_type type: (_) @name) @reference.type
(as_expression right: (_) @name) @reference.type
(is_expression right: (_) @name) @reference.type
(attribute name: (_) @name) @reference.type

; The interface half of an explicit implementation — `string ISpeaker.Greet()`.
; The *member* drops the qualifier (cs.go says why), so this is what keeps the
; interface it satisfies navigable: the declaration names a type, and it is
; recorded as a reference to it like any other.
(explicit_interface_specifier (_) @name) @reference.type
