ALTER TABLE project_visual_node
    DROP CONSTRAINT project_visual_node_type_check,
    ADD CONSTRAINT project_visual_node_type_check
        CHECK (type IN ('character', 'scene', 'ui_element', 'prop', 'reference', 'gameplay_note', 'generated_variant', 'animation', 'video'));

ALTER TABLE project_visual_node
    ADD COLUMN implementation_path text NOT NULL DEFAULT '',
    ADD COLUMN implementation_note text NOT NULL DEFAULT '';
