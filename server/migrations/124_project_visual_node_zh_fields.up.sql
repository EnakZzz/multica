ALTER TABLE project_visual_node
    ADD COLUMN title_zh text NOT NULL DEFAULT '',
    ADD COLUMN description_zh text NOT NULL DEFAULT '',
    ADD COLUMN prompt_zh text NOT NULL DEFAULT '',
    ADD COLUMN result_note_zh text NOT NULL DEFAULT '',
    ADD COLUMN generation_error_zh text NOT NULL DEFAULT '';
