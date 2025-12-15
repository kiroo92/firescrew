-- 添加 direction 列到 motion_snapshots 表
-- 用于跟踪物体移动方向（进入/离开）

-- 检查列是否已存在，如果不存在则添加
DO $$ 
BEGIN
    IF NOT EXISTS (
        SELECT 1 
        FROM information_schema.columns 
        WHERE table_name = 'motion_snapshots' 
        AND column_name = 'direction'
    ) THEN
        ALTER TABLE motion_snapshots 
        ADD COLUMN direction VARCHAR(20) DEFAULT 'unknown';
        
        RAISE NOTICE 'Column direction added successfully';
    ELSE
        RAISE NOTICE 'Column direction already exists';
    END IF;
END $$;

-- 创建索引以优化按方向查询
CREATE INDEX IF NOT EXISTS idx_motion_snapshots_direction ON motion_snapshots(direction);

-- 显示表结构
\d motion_snapshots;

