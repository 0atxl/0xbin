ALTER TABLE live_rooms ADD COLUMN layout_preset TEXT NOT NULL DEFAULT 'single'
    CHECK(layout_preset IN ('single', 'columns', 'rows', 'three', 'grid'));
