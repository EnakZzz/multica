ALTER TABLE project_visual_node
    DROP CONSTRAINT project_visual_node_type_check,
    ADD CONSTRAINT project_visual_node_type_check
        CHECK (type IN ('character', 'scene', 'ui_element', 'prop', 'reference', 'gameplay_note', 'generated_variant'));
