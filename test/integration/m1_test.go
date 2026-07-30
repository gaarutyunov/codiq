// Package integration runs the CodiQ feature suite against the real stack.
//
// SPEC.md §13: godog + testcontainers-go under `go test`, the feature files are
// the single source of the behaviour, and there are no shims — every scenario
// exercises postgres:19beta2, the committed migrations, gopgql, and
// deploy/seed/seed.sql. Nothing is faked and there is no skip path: if Docker is
// not there, the suite fails rather than passing vacuously.
//
// The stack is stood up once for the whole suite and shared by every scenario.
// It is read-only in every scenario but the seed, and the seed is idempotent by
// construction (deploy/seed/seed.sql deletes its own files' rows before
// rewriting them), so scenarios do not need a per-scenario database reset —
// which is what keeps the suite's cost the cost of one gopgql build.
package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/cucumber/godog"
	"github.com/jackc/pgx/v5/pgxpool"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tc "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	// The database the compose file describes (deploy/docker-compose.yml), named
	// here so the DSN the containers use is the DSN the operator would use.
	dbName = "codiq"
	dbUser = "codiq"
	dbPass = "codiq"

	// Network alias for postgres, so the gopgql containers reach it by name the
	// way they do under compose.
	pgAlias = "postgres"

	// Where the SDL and the migrations are mounted inside the gopgql containers,
	// matching deploy/docker-compose.yml so a failure here is reproducible with
	// `docker compose up`.
	sdlPath        = "/schema/codiq.graphql"
	migrationsPath = "/migrations"

	// The image built from deploy/gopgql.Dockerfile. A fixed tag rather than a
	// generated one: the tag is what lets Docker's layer cache turn the second
	// and every later run of this suite into a no-op build instead of another
	// `go install` over the network.
	gopgqlImage = "codiq-test/gopgql:m1"

	// gopgql conform's exit statuses (gopgql cmd/gopgql/main.go): 0 conforms,
	// 1 could not run, 2 drifted. The split is the reason the check is worth
	// running — "no" and "no answer" need different fixes.
	conformExitFailure = 1
	conformExitDrift   = 2
)

// The stack, stood up by TestFeatures and shared by every scenario.
var (
	repoRoot string
	// dsn reaches postgres from inside the Docker network (the gopgql
	// containers); connString reaches it from the test process on the host.
	dsn        string
	connString string
	pool       *pgxpool.Pool
	mcpURL     string
	// nwName is the Docker network postgres and the gopgql containers share, so
	// a scenario that needs a one-shot gopgql run can join it.
	nwName string
)

// TestFeatures is the godog entry point under `go test` (SPEC.md §13).
func TestFeatures(t *testing.T) {
	ctx := context.Background()
	repoRoot = mustRepoRoot(t)

	startStack(t, ctx)

	suite := godog.TestSuite{
		Name:                "m1",
		ScenarioInitializer: InitializeScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{filepath.Join(repoRoot, "features")},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("m1 feature scenarios failed")
	}
}

// startStack brings up the M1 composition: a network, postgres, the gopgql
// image built from deploy/gopgql.Dockerfile, a one-shot `gopgql migrate`, and
// `gopgql-mcp` over HTTP. It mirrors deploy/docker-compose.yml service for
// service; the seed is left to a scenario step, because seeding is behaviour
// the feature file talks about rather than infrastructure.
func startStack(t *testing.T, ctx context.Context) {
	t.Helper()

	nw, err := network.New(ctx)
	require.NoError(t, err, "create docker network")
	t.Cleanup(func() { _ = nw.Remove(context.Background()) })
	nwName = nw.Name

	// postgres:19beta2, pinned exactly: SQL/PGQ is a PostgreSQL 19 feature and
	// there is no fallback on 18 (SPEC.md §10). The initdb script is the one the
	// compose mounts, so a syntax error in it fails here too.
	pgc, err := postgres.Run(ctx, "postgres:19beta2",
		postgres.WithDatabase(dbName),
		postgres.WithUsername(dbUser),
		postgres.WithPassword(dbPass),
		postgres.WithInitScripts(filepath.Join(repoRoot, "deploy", "initdb", "01-dbos.sql")),
		network.WithNetwork([]string{pgAlias}, nw),
		tc.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(3*time.Minute),
		),
	)
	require.NoError(t, err, "start postgres:19beta2")
	t.Cleanup(func() { _ = pgc.Terminate(context.Background()) })

	dsn = fmt.Sprintf("postgres://%s:%s@%s:5432/%s?sslmode=disable", dbUser, dbPass, pgAlias, dbName)
	connString, err = pgc.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err, "postgres connection string")

	pool, err = pgxpool.New(ctx, connString)
	require.NoError(t, err, "open pool")
	t.Cleanup(pool.Close)

	// The schema, applied by the real gopgql from the committed migrations —
	// the same `gopgql migrate --dir` the compose runs, with no --sdl, so the
	// container applies exactly what is in the tree.
	//
	// This is also where the gopgql image is built. deploy/gopgql.Dockerfile
	// installs both binaries from a pinned commit; the build context is a temp
	// directory holding only that Dockerfile, because the Dockerfile reads
	// nothing from the context and the repository root carries docs/node_modules.
	buildStart := time.Now()
	migrate, err := tc.GenericContainer(ctx, tc.GenericContainerRequest{
		ContainerRequest: tc.ContainerRequest{
			FromDockerfile: tc.FromDockerfile{
				Context:    dockerfileContext(t),
				Dockerfile: "gopgql.Dockerfile",
				Repo:       "codiq-test/gopgql",
				Tag:        "m1",
				KeepImage:  true,
			},
			Networks:   []string{nw.Name},
			Env:        map[string]string{"GOPGQL_DSN": dsn},
			Files:      migrationFiles(t),
			Cmd:        []string{"gopgql", "migrate", "--dir", migrationsPath},
			WaitingFor: wait.ForExit().WithExitTimeout(2 * time.Minute),
		},
		Started: true,
	})
	require.NoError(t, err, "run gopgql migrate")
	t.Cleanup(func() { _ = migrate.Terminate(context.Background()) })
	t.Logf("gopgql image built and migrations applied in %s", time.Since(buildStart).Round(time.Millisecond))

	requireExitZero(t, ctx, migrate, "gopgql migrate")

	// The read surface (SPEC.md §8): GraphQL compiled to SQL/PGQ, served over
	// the streamable HTTP transport. It reads the SDL, not the database, to know
	// what is queryable, so the SDL is what gets copied in.
	mcpC, err := tc.GenericContainer(ctx, tc.GenericContainerRequest{
		ContainerRequest: tc.ContainerRequest{
			Image:    gopgqlImage,
			Networks: []string{nw.Name},
			Env: map[string]string{
				"GOPGQL_DSN":       dsn,
				"GOPGQL_TRANSPORT": "http",
				"GOPGQL_ADDR":      ":8080",
			},
			Files:        []tc.ContainerFile{sdlFile()},
			ExposedPorts: []string{"8080/tcp"},
			Cmd:          []string{"gopgql-mcp", "--sdl", sdlPath},
			WaitingFor: wait.ForHTTP("/healthz").
				WithPort("8080/tcp").
				WithStartupTimeout(time.Minute),
		},
		Started: true,
	})
	require.NoError(t, err, "start gopgql-mcp")
	t.Cleanup(func() { _ = mcpC.Terminate(context.Background()) })

	endpoint, err := mcpC.PortEndpoint(ctx, "8080/tcp", "http")
	require.NoError(t, err, "gopgql-mcp endpoint")
	mcpURL = endpoint + "/mcp"
}

// scenarioState carries the MCP session and the last answer between the steps
// of one scenario.
type scenarioState struct {
	session *mcpsdk.ClientSession
	// answer is the JSON document the query tool returned.
	answer string
	// containsEdges counts the last `contains` match, by destination label.
	containsEdges  map[string]int
	containsTotal  int
	conformExit    int
	conformMessage string
}

// InitializeScenario runs once per scenario. Each one opens its own MCP session
// — a real initialize / notifications/initialized handshake over the streamable
// HTTP transport, carrying the session id the server issues — so the protocol
// surface is exercised by every scenario and not once for the suite.
func InitializeScenario(sc *godog.ScenarioContext) {
	st := &scenarioState{}

	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "codiq-m1-tests", Version: "0.1.0"}, nil)
		session, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{Endpoint: mcpURL}, nil)
		if err != nil {
			return ctx, fmt.Errorf("mcp handshake with %s: %w", mcpURL, err)
		}
		st.session = session
		return ctx, nil
	})

	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		if st.session != nil {
			_ = st.session.Close()
			st.session = nil
		}
		return ctx, nil
	})

	sc.Step(`^the CodiQ stack is running$`, st.stackIsRunning)
	sc.Step(`^the demo corpus is seeded$`, st.corpusIsSeeded)
	sc.Step(`^the database holds exactly these tables:$`, st.databaseHoldsTables)
	sc.Step(`^the migration history is recorded$`, st.migrationHistoryRecorded)
	sc.Step(`^the property graph "([^"]*)" is queryable$`, st.graphIsQueryable)
	sc.Step(`^an agent asks over MCP:$`, st.agentAsks)
	sc.Step(`^the answer is:$`, st.answerIs)
	sc.Step(`^the graph is matched on the "([^"]*)" relationship$`, st.matchRelationship)
	sc.Step(`^it yields (\d+) edges$`, st.yieldsEdges)
	sc.Step(`^(\d+) of them lead to a scope$`, st.leadToScope)
	sc.Step(`^(\d+) of them lead to an occurrence$`, st.leadToOccurrence)
	sc.Step(`^the database is checked against the SDL$`, st.checkConform)
	sc.Step(`^no drift is reported$`, st.noDrift)
}

// stackIsRunning confirms both halves of the stack answer before a scenario
// blames them for something else: the database accepts a query, and gopgql-mcp
// has completed a handshake (the Before hook).
func (st *scenarioState) stackIsRunning(ctx context.Context) error {
	var one int
	if err := pool.QueryRow(ctx, `SELECT 1`).Scan(&one); err != nil {
		return fmt.Errorf("postgres not reachable: %w", err)
	}
	if st.session == nil {
		return fmt.Errorf("no MCP session")
	}
	return nil
}

// corpusIsSeeded runs deploy/seed/seed.sql — the file the compose's seed service
// runs, not a copy of it. It is idempotent, so a scenario may state it without
// caring whether an earlier one already did.
func (st *scenarioState) corpusIsSeeded(ctx context.Context) error {
	seed, err := os.ReadFile(filepath.Join(repoRoot, "deploy", "seed", "seed.sql"))
	if err != nil {
		return fmt.Errorf("read seed: %w", err)
	}
	// No arguments, so pgx sends this over the simple protocol, which is what
	// carries a multi-statement script with its own BEGIN/COMMIT.
	if _, err := pool.Exec(ctx, string(seed)); err != nil {
		return fmt.Errorf("apply seed: %w", err)
	}
	return nil
}

func (st *scenarioState) databaseHoldsTables(ctx context.Context, table *godog.Table) error {
	want := make([]string, 0, len(table.Rows)-1)
	for _, row := range table.Rows[1:] {
		want = append(want, row.Cells[0].Value)
	}
	sort.Strings(want)

	// goose's own bookkeeping is excluded: it is the migration runner's table,
	// not part of the model, and it is asserted separately.
	rows, err := pool.Query(ctx, `
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = 'public' AND table_type = 'BASE TABLE'
		  AND table_name <> 'goose_db_version'
		ORDER BY table_name`)
	if err != nil {
		return fmt.Errorf("list tables: %w", err)
	}
	defer rows.Close()

	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		got = append(got, name)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	return check(func(t assert.TestingT) {
		assert.Equal(t, want, got, "the tables gopgql generated from the SDL")
	})
}

func (st *scenarioState) migrationHistoryRecorded(ctx context.Context) error {
	var applied int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM goose_db_version WHERE version_id > 0`).Scan(&applied); err != nil {
		return fmt.Errorf("read goose history: %w", err)
	}
	return check(func(t assert.TestingT) {
		assert.Equal(t, len(migrationNames()), applied,
			"migrations recorded in goose_db_version")
	})
}

// graphIsQueryable proves SQL/PGQ is usable against the named graph, by running
// the cheapest possible GRAPH_TABLE over it. A graph that exists in the catalog
// but cannot be matched would pass a catalog lookup and fail every read.
func (st *scenarioState) graphIsQueryable(ctx context.Context, name string) error {
	// The graph name is a schema identifier, not user data, and cannot be bound
	// as a parameter inside GRAPH_TABLE.
	if name != "app_graph" {
		return fmt.Errorf("unexpected graph name %q", name)
	}
	var n int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM GRAPH_TABLE (app_graph
		  MATCH (v IS occurrence)
		  COLUMNS (v.id AS id))`).Scan(&n); err != nil {
		return fmt.Errorf("graph %q not queryable: %w", name, err)
	}
	return nil
}

// agentAsks calls gopgql's `query` tool over MCP with the GraphQL operation from
// the docstring. It is a real tool call on a real session, not SQL behind the
// server's back — the MCP surface is what M1 ships.
func (st *scenarioState) agentAsks(ctx context.Context, query *godog.DocString) error {
	res, err := st.session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "query",
		Arguments: map[string]any{"query": query.Content},
	})
	if err != nil {
		return fmt.Errorf("call query tool: %w", err)
	}
	text, err := toolText(res)
	if err != nil {
		return err
	}
	if res.IsError {
		return fmt.Errorf("query tool reported an error: %s", text)
	}
	st.answer = text
	return nil
}

func (st *scenarioState) answerIs(want *godog.DocString) error {
	if st.answer == "" {
		return fmt.Errorf("no answer recorded; the MCP step did not run")
	}
	return check(func(t assert.TestingT) {
		assert.JSONEq(t, want.Content, st.answer)
	})
}

// matchRelationship walks one relationship label in the property graph and
// records how many edges it yielded per destination label. The label, not the
// table, is what is matched — which is the whole point when one label spans two
// tables (schema/codiq.graphql, `contains`).
func (st *scenarioState) matchRelationship(ctx context.Context, label string) error {
	if label != "contains" {
		return fmt.Errorf("unsupported relationship %q", label)
	}
	st.containsEdges = map[string]int{}

	// One query per destination label, plus one unlabelled destination for the
	// total: the total is what proves the match spans both tables, and the
	// per-label counts are what prove neither table was the only one reached.
	queries := map[string]string{
		"": `SELECT count(*) FROM GRAPH_TABLE (app_graph
		       MATCH (s IS scope) -[e IS contains]-> (t)
		       COLUMNS (s.id AS source_id))`,
		"scope": `SELECT count(*) FROM GRAPH_TABLE (app_graph
		       MATCH (s IS scope) -[e IS contains]-> (t IS scope)
		       COLUMNS (s.id AS source_id))`,
		"occurrence": `SELECT count(*) FROM GRAPH_TABLE (app_graph
		       MATCH (s IS scope) -[e IS contains]-> (t IS occurrence)
		       COLUMNS (s.id AS source_id))`,
	}
	for dest, q := range queries {
		var n int
		if err := pool.QueryRow(ctx, q).Scan(&n); err != nil {
			return fmt.Errorf("match contains -> %q: %w", dest, err)
		}
		if dest == "" {
			st.containsTotal = n
			continue
		}
		st.containsEdges[dest] = n
	}
	return nil
}

func (st *scenarioState) yieldsEdges(want int) error {
	return check(func(t assert.TestingT) {
		assert.Equal(t, want, st.containsTotal,
			"edges reached by one match on the label, across both tables")
	})
}

func (st *scenarioState) leadToScope(want int) error {
	return check(func(t assert.TestingT) {
		assert.Equal(t, want, st.containsEdges["scope"], "contains_scope edges")
	})
}

func (st *scenarioState) leadToOccurrence(want int) error {
	return check(func(t assert.TestingT) {
		assert.Equal(t, want, st.containsEdges["occurrence"], "contains_occurrence edges")
	})
}

// checkConform runs `gopgql conform` against the live database: it reads the
// property graph back out of PostgreSQL and reports how it differs from the
// SDL. Its answer is its exit status, so that is what is captured.
func (st *scenarioState) checkConform(ctx context.Context) error {
	ctr, err := tc.GenericContainer(ctx, tc.GenericContainerRequest{
		ContainerRequest: tc.ContainerRequest{
			Image:      gopgqlImage,
			Networks:   []string{nwName},
			Env:        map[string]string{"GOPGQL_DSN": dsn},
			Files:      []tc.ContainerFile{sdlFile()},
			Cmd:        []string{"gopgql", "conform", "--sdl", sdlPath},
			WaitingFor: wait.ForExit().WithExitTimeout(time.Minute),
		},
		Started: true,
	})
	if err != nil {
		return fmt.Errorf("run gopgql conform: %w", err)
	}
	defer func() { _ = ctr.Terminate(context.Background()) }()

	state, err := ctr.State(ctx)
	if err != nil {
		return fmt.Errorf("read conform exit status: %w", err)
	}
	st.conformExit = state.ExitCode
	st.conformMessage = containerLogs(ctx, ctr)
	return nil
}

func (st *scenarioState) noDrift() error {
	switch st.conformExit {
	case 0:
		return nil
	case conformExitDrift:
		return fmt.Errorf("the database has drifted from the SDL:\n%s", st.conformMessage)
	case conformExitFailure:
		return fmt.Errorf("gopgql conform could not run:\n%s", st.conformMessage)
	default:
		return fmt.Errorf("gopgql conform exited %d:\n%s", st.conformExit, st.conformMessage)
	}
}

// --- helpers ---------------------------------------------------------------

// check runs testify assertions outside a *testing.T — godog owns the failure
// reporting, so an assertion's message is turned into the step's error.
func check(fn func(t assert.TestingT)) error {
	c := &collector{}
	fn(c)
	return c.err
}

type collector struct{ err error }

func (c *collector) Errorf(format string, args ...any) {
	if c.err == nil {
		c.err = fmt.Errorf(format, args...)
	}
}

// toolText joins the text content of an MCP tool result.
func toolText(res *mcpsdk.CallToolResult) (string, error) {
	var out string
	for _, c := range res.Content {
		if tc, ok := c.(*mcpsdk.TextContent); ok {
			out += tc.Text
		}
	}
	if out == "" {
		raw, _ := json.Marshal(res)
		return "", fmt.Errorf("tool result carried no text content: %s", raw)
	}
	return out, nil
}

// mustRepoRoot resolves the repository root from the test's working directory,
// which `go test` sets to the package directory.
func mustRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	root, err := filepath.Abs(filepath.Join(wd, "..", ".."))
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(root, "schema", "codiq.graphql"), "repository root")
	return root
}

// dockerfileContext copies deploy/gopgql.Dockerfile into an empty directory and
// returns it as the build context. The Dockerfile installs gopgql from a pinned
// commit over the network and reads nothing from the context, so an empty
// context is the honest one — and it keeps docs/node_modules out of the tarball
// that would otherwise be sent to the daemon.
func dockerfileContext(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(repoRoot, "deploy", "gopgql.Dockerfile"))
	require.NoError(t, err, "read deploy/gopgql.Dockerfile")
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gopgql.Dockerfile"), src, 0o644))
	return dir
}

// migrationFiles lists schema/migrations/*.sql as container files under
// /migrations, in the order goose will apply them.
func migrationFiles(t *testing.T) []tc.ContainerFile {
	t.Helper()
	names := migrationNames()
	require.NotEmpty(t, names, "schema/migrations holds no .sql files")
	files := make([]tc.ContainerFile, 0, len(names))
	for _, name := range names {
		files = append(files, tc.ContainerFile{
			HostFilePath:      filepath.Join(repoRoot, "schema", "migrations", name),
			ContainerFilePath: migrationsPath + "/" + name,
			FileMode:          0o644,
		})
	}
	return files
}

// migrationNames lists the committed migrations, sorted by version.
func migrationNames() []string {
	entries, err := os.ReadDir(filepath.Join(repoRoot, "schema", "migrations"))
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".sql" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// sdlFile is schema/codiq.graphql, mounted where the compose mounts it.
func sdlFile() tc.ContainerFile {
	return tc.ContainerFile{
		HostFilePath:      filepath.Join(repoRoot, "schema", "codiq.graphql"),
		ContainerFilePath: sdlPath,
		FileMode:          0o644,
	}
}

// requireExitZero fails the test when a one-shot container exited non-zero,
// with its output — a migration that did not apply is not worth chasing through
// the scenarios that follow it.
func requireExitZero(t *testing.T, ctx context.Context, ctr tc.Container, what string) {
	t.Helper()
	state, err := ctr.State(ctx)
	require.NoError(t, err, "read %s exit status", what)
	require.Zerof(t, state.ExitCode, "%s exited %d:\n%s", what, state.ExitCode, containerLogs(ctx, ctr))
}

func containerLogs(ctx context.Context, ctr tc.Container) string {
	rc, err := ctr.Logs(ctx)
	if err != nil {
		return fmt.Sprintf("(logs unavailable: %v)", err)
	}
	defer func() { _ = rc.Close() }()
	out, err := io.ReadAll(rc)
	if err != nil {
		return fmt.Sprintf("(logs unreadable: %v)", err)
	}
	return string(out)
}
