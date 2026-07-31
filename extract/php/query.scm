; PHP stanza — tree-sitter query half (SPEC.md §5).
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
; This file says *where* things are. php.go says what they are called: it builds
; the SCIP descriptor suffix by walking each captured node's ancestors, assigns
; role and symbol_kind, and resolves same-file references. Anything that needs
; another file is emitted unresolved (§4.3).
;
; Two properties of this grammar shape the whole file, and both make it shorter
; than C#'s.
;
; The first is that a *name* and a *variable* are different tokens. `(name)` is
; an identifier — a class, a function, a constant, a namespace segment — and
; `(variable_name)` is `$x`, always with its own `(name)` child. So the question
; that forced Ruby to apply the language's own local-vs-send rule at every bare
; identifier does not arise: PHP writes the sigil, and the CST carries it.
;
; The second is that a type position is a real node. `(named_type)` wraps the
; name written in a parameter, a property or a return type, so `@reference.type`
; can be a pattern over a node kind and not — as in C#, whose grammar has no
; `type_identifier` — one pattern per position a type may appear in. The three
; places PHP writes a type *without* the wrapper are `base_clause`,
; `class_interface_clause` and `attribute`, and those three are listed by hand.

; ---------------------------------------------------------------- scopes -----
;
; PHP's lexical scopes, and there are fewer of them than the C-family shape
; suggests. PHP has **no block scoping at all**: a variable assigned inside an
; `if`, a `for`, a `foreach` or a `while` is visible after it in the enclosing
; function, exactly as in Ruby and Python. So `(compound_statement)` is
; deliberately *not* a scope — capturing it would model something the language
; does not have, and php.go resolves same-file references by asking which scope a
; definition was declared in.
;
; What *is* a scope is a function boundary, because PHP's are hard: a function
; body sees no enclosing local at all, not even a global one, without `global` or
; a `use (…)` clause. `(anonymous_function)` and `(arrow_function)` are function
; scopes for the same reason, and the fact that an arrow function *does* capture
; by value is a difference this model does not need to represent.
;
; `@scope.package` is the `namespace … { }` block, which is a real lexical
; region — the C# block-namespace case, two languages on. The `namespace X;`
; form delimits nothing (everything after it is its sibling in the CST) and is
; captured as a definition below and not as a scope.

(program) @scope.file
(namespace_definition body: (compound_statement)) @scope.package
(class_declaration) @scope.type
(interface_declaration) @scope.type
(trait_declaration) @scope.type
(enum_declaration) @scope.type
(anonymous_class) @scope.type
(function_definition) @scope.function
(method_declaration) @scope.function
(anonymous_function) @scope.function
(arrow_function) @scope.function

; ----------------------------------------------------------- definitions -----
;
; The file itself is the definition of a package, as it is in every language
; here. PHP spells the declaration two ways and, like C#, a file's namespace is
; therefore not a single fact about the file:
;
;   * `namespace Foo\Bar;`      — everything after it is its sibling.
;   * `namespace Foo\Bar { … }` — a block; a file may hold several side by side,
;                                 and one of them may be the *global* namespace,
;                                 written `namespace { … }` with no name at all.
;   * `(program)`               — neither, which is the global namespace.
;
; Both spellings are one node kind here, told apart by whether it has a `body`,
; so both are covered by the one pattern and php.go asks.
;
; The kind is the neutral core's `package` because that is the kind link's
; `imports` derivation joins on (store/sqlc/query.sql). Not `module`: a kind of
; `module` derives no edge and does so *silently*, which is the trap TypeScript
; fell into and Ruby was one line from — php_test.go guards it by name.

(program) @definition.package
(namespace_definition name: (namespace_name) @name) @definition.package

; The type-declaring forms. An interface is captured as `interface` directly
; rather than refined afterwards — the CST already says which it is, and link
; keys `implements` off that kind — while a class, an enum and a **trait** are
; captured as `type`.
;
; The trait is the interesting one and php.go's package comment argues it at
; length. A trait is not an interface (it demands nothing and cannot be
; type-hinted) and not a namespace (nothing may be written `Loudly\x`), so it is
; neither Ruby's `package` reading of a module nor C#'s `interface`. It is a
; named container of members, which is what the neutral core's `type` means.
(class_declaration name: (name) @name) @definition.type
(enum_declaration name: (name) @name) @definition.type
(trait_declaration name: (name) @name) @definition.type
(interface_declaration name: (name) @name) @definition.interface

; Callables. A method is a member of a type; a `function_definition` is not, and
; php.go's refineKind splits them — a `function` written inside a method is a
; *nested* function definition in PHP and is still global once the outer call
; runs, but its descriptor carries the enclosing callable, which falls out of the
; ancestor walk with no special case.
;
; `__construct` is captured like any other method and is deliberately not
; special-cased. Unlike Java's, C#'s and Rust's, PHP's constructor has a fixed
; name that is not the type's, so it neither collides with the type nor needs a
; component of its own — and `new Greeter(…)` is descriptored as the *type* it
; names, because that is what the reference site writes.
(function_definition name: (name) @name) @definition.function
(method_declaration name: (name) @name) @definition.method

; State. A property, a class constant, a global constant and an enum case are
; all state a container holds, and each is written down at exactly one site.
;
; `const` is `constant` and a property is `field`, which is the split every
; stanza here draws; an enum case is a `constant` because that is what it is —
; `Suit::Hearts` is reached exactly as `Suit::DEFAULT` is, and the syntax gives
; a reference no way to tell them apart either.
(property_declaration (property_element name: (variable_name) @name)) @definition.field
(const_declaration (const_element (name) @name)) @definition.constant
(enum_case name: (name) @name) @definition.constant

; Bindings. PHP has no declaration form for a local: the assignment is the
; declaration, exactly as in Ruby and Python. `static $x` inside a function and
; `global $x` are the two forms that *are* declarations, and both are captured
; here because each states a name the assignment patterns would otherwise attach
; to the wrong scope.
(assignment_expression left: (variable_name) @name) @definition.variable
(assignment_expression left: (list_literal (variable_name) @name)) @definition.variable
(augmented_assignment_expression left: (variable_name) @name) @definition.variable
(static_variable_declaration name: (variable_name) @name) @definition.variable
(catch_clause name: (variable_name) @name) @definition.variable
(foreach_statement (variable_name) @name) @definition.variable
(foreach_statement (pair (variable_name) @name)) @definition.variable

; Parameters, in the three shapes PHP writes one. A promoted constructor
; parameter is a parameter *and* a property; php.go's refineKind promotes it to a
; field for the reason java.go and cs.go promote a record component, because
; `$this->name` reaches it and a parameter descriptor is not what that writes.
(simple_parameter name: (variable_name) @name) @definition.parameter
(variadic_parameter name: (variable_name) @name) @definition.parameter
(property_promotion_parameter name: (variable_name) @name) @definition.parameter

; --------------------------------------------------------------- imports -----
;
; `use` at the top level of a file — never inside a class body, which is a
; different node and a different thing — and it is PHP's whole import story.
;
;   * `use A\B\C;`             — a class, an interface, a trait or an enum.
;   * `use A\B\C as D;`        — the same, under another simple name.
;   * `use function A\B\f;`    — a function.
;   * `use const A\B\K;`       — a constant.
;   * `use A\B\{C, D as E};`   — a group, which is the four above factored.
;
; The declaration is captured whole rather than its clauses, because the group
; form's prefix is a sibling of the group and php.go needs both.
;
; What makes this the occurrence link's `imports` derivation joins against is
; that the namespace half is *stated*: `use A\B\C;` names `A\B\C` and PHP says
; the last segment is the imported name, so the namespace is everything before
; it and there is no convention to lean on and no split to guess at. That is C#'s
; position rather than Java's, and it is better than C#'s in one respect — a C#
; `using` imports a namespace and its alias form imports a type, so cs.go must
; look at the shape of the directive; PHP's `use` *always* imports a name, so the
; split is the same in all five spellings.
;
; `require`, `require_once`, `include` and `include_once` are deliberately not
; imports here. php.go says why at length: they name a path, PHP has no second
; path-keyed namespace for them to join against, and giving them one would
; reintroduce exactly the collision the mixed-language corpus exists to detect.

(namespace_use_declaration) @import

; ------------------------------------------------------------ references -----
;
; A call and a plain read can capture the same identifier — `$g->greet()` yields
; both a @reference.call on `greet` and, on `$g`, a @reference.read. php.go
; dedupes by identifier range, preferring the more specific role, and drops any
; reference landing on a node a definition or a `use` declaration already
; claimed.
;
; PHP writes member access with two operators and — this is the point on which
; this stanza differs from every reading the syntax invites — **they do not
; separate static members from instance members**. `parent::__construct()` calls
; an instance method and is the commonest `::` in all of PHP; `$obj->stat()`
; reaches a static one. The two operators distinguish the *binding*, not the
; member namespace, and PHP forbids a class declaring a static and an instance
; member of one name. So both render the same descriptor and php.go carries no
; static qualifier. See its package comment, which contrasts this with Ruby's
; `self.`.
;
; Only reads. `@reference.write` has no landing site in the core model: the
; occurrence table records role `definition` or `reference` and nothing else, so
; an assignment's left-hand side is a reference like any other.

(function_call_expression function: (name) @name) @reference.call
(function_call_expression function: (qualified_name) @name) @reference.call
(function_call_expression function: (relative_name) @name) @reference.call
(member_call_expression name: (name) @name) @reference.call
(nullsafe_member_call_expression name: (name) @name) @reference.call
(scoped_call_expression name: (name) @name) @reference.call

(member_access_expression name: (name) @name) @reference.read
(nullsafe_member_access_expression name: (name) @name) @reference.read
(scoped_property_access_expression name: (variable_name) @name) @reference.read
(variable_name) @reference.read

; `Foo::CONST` and `Suit::Hearts`, which is the one node kind in this grammar
; with no field names on its children: both halves are bare `(name)`, and a
; pattern that bound either would bind both. So the node is captured whole under
; a role of its own and php.go splits it positionally — first named child is the
; class, last is the member — which is the same thing cs.go does for a type
; expression and for the same reason.
(class_constant_access_expression) @reference.classconst

; Types, in the positions the grammar puts one. `(named_type)` covers a
; parameter, a property, a return type and a `catch` — every place PHP writes a
; type annotation — because the grammar wraps all of them. The rest are the
; places a type is written *without* the wrapper, and `class_interface_clause` is
; the one that matters most: PHP declares interface satisfaction explicitly, and
; this is where the declaration is recorded — as what it structurally is, a
; reference to the interface's type, which needs no edge kind the schema does not
; already have.
;
; Every pattern binds @name to the *path* node — `(name)`, `(qualified_name)` or
; `(relative_name)` — rather than to the bare identifier, because the path is
; what decides the descriptor and the identifier is only what the occurrence
; covers. php.go reduces the one to the other.
;
; `use_declaration` is the trait use inside a class body, and it is a type
; reference rather than an @import for the reason php.go gives at length: a trait
; is a type and not a namespace, so it derives no `imports` edge — and it derives
; no `implements` edge either, which is this stanza's one honest false negative.
(named_type (_) @name) @reference.type
(base_clause (name) @name) @reference.type
(base_clause (qualified_name) @name) @reference.type
(class_interface_clause (name) @name) @reference.type
(class_interface_clause (qualified_name) @name) @reference.type
(object_creation_expression (name) @name) @reference.type
(object_creation_expression (qualified_name) @name) @reference.type
(object_creation_expression (relative_name) @name) @reference.type
(use_declaration (name) @name) @reference.type
(use_declaration (qualified_name) @name) @reference.type
(attribute (name) @name) @reference.type
(attribute (qualified_name) @name) @reference.type

; The scope half of `Foo::bar()` and `Foo::$prop`, which names a class and can
; name nothing else — PHP has no namespace on the left of a `::`. It is the one
; place the language settles for free a question C# needed a whole `splitPath`
; for. (`Foo::CONST`'s scope half is covered by @reference.classconst above,
; because that node labels neither of its children.)
(scoped_call_expression scope: (name) @name) @reference.type
(scoped_call_expression scope: (qualified_name) @name) @reference.type
(scoped_property_access_expression scope: (name) @name) @reference.type
(scoped_property_access_expression scope: (qualified_name) @name) @reference.type
