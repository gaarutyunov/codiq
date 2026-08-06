-- +goose Up
CREATE PROPERTY GRAPH app_graph
  VERTEX TABLES (
    file LABEL file PROPERTIES (id, corpus, path, lang, pkg_scheme, pkg_manager, pkg_name, pkg_version),
    occurrence LABEL occurrence PROPERTIES (id, file_id, descriptor, role, symbol_kind, name, range_start, range_end, scope_id),
    scope LABEL scope PROPERTIES (id, file_id, kind, range_start, range_end, parent_scope_id)
  )
  EDGE TABLES (
    calls SOURCE KEY (source_id) REFERENCES occurrence (id)
            DESTINATION KEY (target_id) REFERENCES occurrence (id)
            LABEL calls PROPERTIES (source_id, target_id),
    contains_occurrence SOURCE KEY (source_id) REFERENCES scope (id)
            DESTINATION KEY (target_id) REFERENCES occurrence (id)
            LABEL contains PROPERTIES (source_id, target_id),
    contains_scope SOURCE KEY (source_id) REFERENCES scope (id)
            DESTINATION KEY (target_id) REFERENCES scope (id)
            LABEL contains PROPERTIES (source_id, target_id),
    defines SOURCE KEY (source_id) REFERENCES file (id)
            DESTINATION KEY (target_id) REFERENCES occurrence (id)
            LABEL defines PROPERTIES (source_id, target_id),
    implements SOURCE KEY (source_id) REFERENCES occurrence (id)
            DESTINATION KEY (target_id) REFERENCES occurrence (id)
            LABEL implements PROPERTIES (source_id, target_id),
    imports SOURCE KEY (source_id) REFERENCES file (id)
            DESTINATION KEY (target_id) REFERENCES file (id)
            LABEL imports PROPERTIES (source_id, target_id),
    references_local SOURCE KEY (source_id) REFERENCES occurrence (id)
            DESTINATION KEY (target_id) REFERENCES occurrence (id)
            LABEL references_local PROPERTIES (source_id, target_id),
    resolves_to SOURCE KEY (source_id) REFERENCES occurrence (id)
            DESTINATION KEY (target_id) REFERENCES occurrence (id)
            LABEL resolves_to PROPERTIES (source_id, target_id),
    type_defines SOURCE KEY (source_id) REFERENCES occurrence (id)
            DESTINATION KEY (target_id) REFERENCES occurrence (id)
            LABEL type_defines PROPERTIES (source_id, target_id)
  );

-- +goose Down
DROP PROPERTY GRAPH IF EXISTS app_graph;
