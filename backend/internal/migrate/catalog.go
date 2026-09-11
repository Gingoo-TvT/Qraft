package migrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"sort"
)

// CatalogAlgorithm versions the canonical PostgreSQL catalog representation.
const CatalogAlgorithm = "pg-catalog-v1"

const catalogSnapshotSQL = `
WITH control_relations AS (
    SELECT c.oid, c.reltype
    FROM pg_catalog.pg_class c
    JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
    WHERE n.nspname = 'public'
      AND c.relname IN ('schema_migrations', 'schema_migration_baseline_events')
), catalog_objects AS (
    -- Column order is intentionally not part of the identity. PostgreSQL
    -- cannot place an ADD COLUMN before an existing column without rebuilding
    -- the table; the application schema contract is name-based.
    SELECT
        'column'::text AS kind,
        format('%I.%I.%I', n.nspname, c.relname, a.attname) AS identity,
        concat_ws('|',
            'relkind=' || c.relkind::text,
            'persistence=' || c.relpersistence::text,
            'type=' || pg_catalog.format_type(a.atttypid, a.atttypmod),
            'not_null=' || a.attnotnull::text,
            'identity=' || a.attidentity::text,
            'generated=' || a.attgenerated::text,
            'storage=' || a.attstorage::text,
            'compression=' || a.attcompression::text,
            'collation=' || CASE WHEN coll.oid IS NULL THEN '' ELSE format('%I.%I', cn.nspname, coll.collname) END,
            'default=' || COALESCE(pg_catalog.pg_get_expr(ad.adbin, ad.adrelid, false), '')
        ) AS definition
    FROM pg_catalog.pg_attribute a
    JOIN pg_catalog.pg_class c ON c.oid = a.attrelid
    JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
    LEFT JOIN pg_catalog.pg_attrdef ad ON ad.adrelid = a.attrelid AND ad.adnum = a.attnum
    LEFT JOIN pg_catalog.pg_collation coll ON coll.oid = a.attcollation
    LEFT JOIN pg_catalog.pg_namespace cn ON cn.oid = coll.collnamespace
    WHERE a.attnum > 0
      AND NOT a.attisdropped
      AND c.relkind IN ('r', 'p', 'v', 'm', 'f', 'S')
      AND n.nspname NOT IN ('pg_catalog', 'information_schema')
      AND n.nspname !~ '^pg_(toast|temp)'
      AND c.oid NOT IN (SELECT oid FROM control_relations)

    UNION ALL

    SELECT
        'index',
        format('%I.%I', ni.nspname, ic.relname),
        pg_catalog.pg_get_indexdef(i.indexrelid, 0, false)
    FROM pg_catalog.pg_index i
    JOIN pg_catalog.pg_class ic ON ic.oid = i.indexrelid
    JOIN pg_catalog.pg_namespace ni ON ni.oid = ic.relnamespace
    JOIN pg_catalog.pg_class tc ON tc.oid = i.indrelid
    JOIN pg_catalog.pg_namespace nt ON nt.oid = tc.relnamespace
    WHERE nt.nspname NOT IN ('pg_catalog', 'information_schema')
      AND nt.nspname !~ '^pg_(toast|temp)'
      AND tc.oid NOT IN (SELECT oid FROM control_relations)

    UNION ALL

    SELECT
        'constraint',
        format('%I.%s.%I', n.nspname, COALESCE(c.relname, '-'), con.conname),
        concat_ws('|', 'type=' || con.contype::text, pg_catalog.pg_get_constraintdef(con.oid, false))
    FROM pg_catalog.pg_constraint con
    JOIN pg_catalog.pg_namespace n ON n.oid = con.connamespace
    LEFT JOIN pg_catalog.pg_class c ON c.oid = con.conrelid
    WHERE n.nspname NOT IN ('pg_catalog', 'information_schema')
      AND n.nspname !~ '^pg_(toast|temp)'
      AND (con.conrelid = 0 OR con.conrelid NOT IN (SELECT oid FROM control_relations))

    UNION ALL

    SELECT
        'function',
        format('%I.%I(%s)', n.nspname, p.proname, pg_catalog.pg_get_function_identity_arguments(p.oid)),
        concat_ws('|',
            'kind=' || p.prokind::text,
            'result=' || pg_catalog.pg_get_function_result(p.oid),
            pg_catalog.pg_get_functiondef(p.oid)
        )
    FROM pg_catalog.pg_proc p
    JOIN pg_catalog.pg_namespace n ON n.oid = p.pronamespace
    WHERE n.nspname NOT IN ('pg_catalog', 'information_schema')
      AND n.nspname !~ '^pg_(toast|temp)'
      AND p.prokind IN ('f', 'p', 'w')
      AND NOT (n.nspname = 'public' AND p.proname = 'prevent_schema_migration_baseline_event_mutation')

    UNION ALL

    SELECT
        'trigger',
        format('%I.%I.%I', n.nspname, c.relname, t.tgname),
        pg_catalog.pg_get_triggerdef(t.oid, false)
    FROM pg_catalog.pg_trigger t
    JOIN pg_catalog.pg_class c ON c.oid = t.tgrelid
    JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
    WHERE NOT t.tgisinternal
      AND n.nspname NOT IN ('pg_catalog', 'information_schema')
      AND n.nspname !~ '^pg_(toast|temp)'
      AND c.oid NOT IN (SELECT oid FROM control_relations)

    UNION ALL

    SELECT
        'type',
        format('%I.%I', n.nspname, t.typname),
        concat_ws('|',
            'kind=' || t.typtype::text,
            'category=' || t.typcategory::text,
            'element=' || CASE WHEN t.typelem = 0 THEN '' ELSE pg_catalog.format_type(t.typelem, NULL) END,
            'base=' || CASE WHEN t.typbasetype = 0 THEN '' ELSE pg_catalog.format_type(t.typbasetype, t.typtypmod) END,
            'not_null=' || t.typnotnull::text,
            'default=' || COALESCE(t.typdefault, ''),
            'collation=' || CASE WHEN coll.oid IS NULL THEN '' ELSE format('%I.%I', cn.nspname, coll.collname) END,
            'enum=' || COALESCE((
                SELECT pg_catalog.array_agg(e.enumlabel ORDER BY e.enumsortorder)::text
                FROM pg_catalog.pg_enum e
                WHERE e.enumtypid = t.oid
            ), ''),
            'range_subtype=' || CASE WHEN r.rngsubtype IS NULL THEN '' ELSE pg_catalog.format_type(r.rngsubtype, NULL) END
        )
    FROM pg_catalog.pg_type t
    JOIN pg_catalog.pg_namespace n ON n.oid = t.typnamespace
    LEFT JOIN pg_catalog.pg_collation coll ON coll.oid = t.typcollation
    LEFT JOIN pg_catalog.pg_namespace cn ON cn.oid = coll.collnamespace
    LEFT JOIN pg_catalog.pg_range r ON r.rngtypid = t.oid
    WHERE t.typtype IN ('b', 'd', 'e', 'r', 'm')
      AND n.nspname NOT IN ('pg_catalog', 'information_schema')
      AND n.nspname !~ '^pg_(toast|temp)'
      AND t.typrelid NOT IN (SELECT oid FROM control_relations)
      AND t.typelem NOT IN (SELECT reltype FROM control_relations)
)
SELECT kind, identity, definition
FROM catalog_objects
ORDER BY kind, identity, definition`

type catalogObject struct {
	Kind       string
	Identity   string
	Definition string
}

// CatalogHashResult is a reproducible digest of the non-control catalog.
type CatalogHashResult struct {
	Algorithm   string
	SHA256      string
	ObjectCount int64
}

// CatalogManifestEntry identifies a catalog object without exposing its
// potentially sensitive SQL definition.
type CatalogManifestEntry struct {
	Kind             string `json:"kind"`
	Identity         string `json:"identity"`
	DefinitionSHA256 string `json:"definition_sha256"`
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// CatalogHash computes a consistent catalog digest under the migration lock.
func (r *Runner) CatalogHash(ctx context.Context) (CatalogHashResult, error) {
	objects, err := r.catalogSnapshot(ctx)
	if err != nil {
		return CatalogHashResult{}, err
	}
	return hashCatalogObjects(objects), nil
}

// CatalogManifest returns stable per-object digests from a consistent catalog
// snapshot under the migration lock.
func (r *Runner) CatalogManifest(ctx context.Context) ([]CatalogManifestEntry, error) {
	objects, err := r.catalogSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	return manifestCatalogObjects(objects), nil
}

func (r *Runner) catalogSnapshot(ctx context.Context) ([]catalogObject, error) {
	var objects []catalogObject
	err := r.withLock(ctx, func(conn *sql.Conn) error {
		tx, err := conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if err != nil {
			return fmt.Errorf("begin catalog snapshot: %w", err)
		}
		defer tx.Rollback()

		objects, err = queryCatalogObjects(ctx, tx)
		if err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit catalog snapshot: %w", err)
		}
		return nil
	})
	return objects, err
}

func catalogHash(ctx context.Context, db queryer) (CatalogHashResult, error) {
	objects, err := queryCatalogObjects(ctx, db)
	if err != nil {
		return CatalogHashResult{}, err
	}
	return hashCatalogObjects(objects), nil
}

func queryCatalogObjects(ctx context.Context, db queryer) ([]catalogObject, error) {
	rows, err := db.QueryContext(ctx, catalogSnapshotSQL)
	if err != nil {
		return nil, fmt.Errorf("query catalog snapshot: %w", err)
	}
	defer rows.Close()

	objects := make([]catalogObject, 0)
	for rows.Next() {
		var object catalogObject
		if err := rows.Scan(&object.Kind, &object.Identity, &object.Definition); err != nil {
			return nil, fmt.Errorf("scan catalog snapshot: %w", err)
		}
		objects = append(objects, object)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate catalog snapshot: %w", err)
	}

	return objects, nil
}

func hashCatalogObjects(objects []catalogObject) CatalogHashResult {
	ordered := orderCatalogObjects(objects)

	digest := sha256.New()
	writeHashField(digest, CatalogAlgorithm)
	for _, object := range ordered {
		writeHashField(digest, object.Kind)
		writeHashField(digest, object.Identity)
		writeHashField(digest, object.Definition)
	}
	return CatalogHashResult{
		Algorithm:   CatalogAlgorithm,
		SHA256:      hex.EncodeToString(digest.Sum(nil)),
		ObjectCount: int64(len(ordered)),
	}
}

func manifestCatalogObjects(objects []catalogObject) []CatalogManifestEntry {
	ordered := orderCatalogObjects(objects)
	manifest := make([]CatalogManifestEntry, 0, len(ordered))
	for _, object := range ordered {
		definitionDigest := sha256.Sum256([]byte(object.Definition))
		manifest = append(manifest, CatalogManifestEntry{
			Kind:             object.Kind,
			Identity:         object.Identity,
			DefinitionSHA256: hex.EncodeToString(definitionDigest[:]),
		})
	}
	return manifest
}

func orderCatalogObjects(objects []catalogObject) []catalogObject {
	ordered := append([]catalogObject(nil), objects...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Kind != ordered[j].Kind {
			return ordered[i].Kind < ordered[j].Kind
		}
		if ordered[i].Identity != ordered[j].Identity {
			return ordered[i].Identity < ordered[j].Identity
		}
		return ordered[i].Definition < ordered[j].Definition
	})
	return ordered
}

func writeHashField(target hash.Hash, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	target.Write(length[:])
	target.Write([]byte(value))
}
