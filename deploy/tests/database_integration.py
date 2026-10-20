"""真实PostgreSQL验收，使用独立容器，无宿主端口及生产凭据。"""
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
import shutil
import subprocess
import tempfile
import time
import uuid

ROOT = Path(__file__).resolve().parents[2]
IMAGE = "pgvector/pgvector:pg16"


def run(*args, data=None, check=True):
    result = subprocess.run(args, input=data, capture_output=True, timeout=120)
    if check and result.returncode:
        raise RuntimeError(f"命令失败 {args[:3]}\n{result.stdout.decode(errors='replace')[-2000:]}\n{result.stderr.decode(errors='replace')[-2000:]}")
    return result


def package(target):
    target.mkdir(parents=True)
    for name in ("database.sh", "migrations.list"):
        shutil.copyfile(ROOT / "deploy" / name, target / name)
    (target / "migrations").mkdir()
    for name in (target / "migrations.list").read_text().splitlines():
        shutil.copyfile(ROOT / "scripts/migrations" / name, target / "migrations" / name)


def main():
    container = "solvify-deploy-test-" + uuid.uuid4().hex[:12]
    run("docker", "run", "--detach", "--name", container, "--network", "none",
        "-e", "POSTGRES_HOST_AUTH_METHOD=trust", IMAGE)
    try:
        for _ in range(60):
            if run("docker", "exec", container, "pg_isready", "-U", "postgres", check=False).returncode == 0:
                break
            time.sleep(1)
        else:
            raise RuntimeError("测试PostgreSQL未就绪")

        def sql(db, text):
            return run("docker", "exec", "-i", container, "psql", "-X", "-At", "-U", "postgres",
                       "-d", db, "-v", "ON_ERROR_STOP=1", data=text.encode()).stdout.decode().strip()

        with tempfile.TemporaryDirectory() as temp:
            target = Path(temp) / "release"
            package(target)
            run("docker", "cp", str(target), container + ":/tmp/release")

        def shell(db, body, check=True):
            prelude = """set -Eeuo pipefail
SCRIPT_DIR=/tmp/release
SOLVIFY_ROOT=/tmp/test-root
umask 077
source "$SCRIPT_DIR/database.sh"
log() { printf '%s\\n' "$*"; }
compose_command() {
  [[ "$1 $2 $3" == 'exec -T postgres' ]]
  shift 3
  "$@"
}
"""
            return run("docker", "exec", "-i", "-e", "POSTGRES_USER=postgres", "-e", "POSTGRES_DB=" + db,
                       container, "bash", "-ec", prelude + body, check=check)

        def upgrade(db):
            result = shell(db, 'validate_migration_package\nbackup_database abc1234\nmigrate_database || { cat "$DATABASE_BACKUP_FILE.migration.log"; exit 1; }\nprintf "BACKUP=%s\\n" "$DATABASE_BACKUP_FILE"')
            return next(line[7:] for line in result.stdout.decode().splitlines() if line.startswith("BACKUP="))

        def create(db):
            sql("postgres", "CREATE DATABASE " + db)
            sql(db, (ROOT / "scripts/init_knowledge_schema.sql").read_text(encoding="utf-8"))

        create("fresh")
        upgrade("fresh")
        expected = len((ROOT / "deploy/migrations.list").read_text().splitlines())
        assert sql("fresh", "SELECT count(*) FROM app_schema_migrations") == str(expected)
        print("新库初始化+全部迁移通过", flush=True)

        create("legacy")
        sql("legacy", """
DROP TABLE IF EXISTS context_artifacts;
DROP TABLE mcp_connections;
DROP TABLE mcp_templates;
DROP TABLE app_schema_migrations;
ALTER TABLE chat_messages DROP COLUMN embedding_status, DROP COLUMN embedding_attempts,
 DROP COLUMN embedding_available_at, DROP COLUMN embedding_lease_until,
 DROP COLUMN embedding_lease_token, DROP COLUMN embedding_error;
CREATE TABLE tool_types(id varchar(36) PRIMARY KEY,tool_key text,is_enabled bool);
CREATE TABLE tool_providers(id varchar(36) PRIMARY KEY,tool_type_id varchar(36),provider_key text,name text,
 description text,provider_type text,provider_config jsonb,config_schema jsonb,mcp_tool_manifest jsonb,
 is_enabled bool,is_system bool,display_order int,created_at timestamptz,updated_at timestamptz);
CREATE TABLE user_tool_configs(id varchar(36),user_id varchar(36),provider_id varchar(36),display_name text,
 config jsonb,is_enabled bool,created_at timestamptz,updated_at timestamptz);
INSERT INTO tool_types VALUES ('type1','mcp',true);
INSERT INTO tool_providers VALUES ('provider1','type1','search','测试搜索','测试','mcp',
 '{"mcp":{"url":"https://example.invalid/mcp"}}',NULL,NULL,true,true,0,now(),now());
INSERT INTO user_tool_configs VALUES ('conn1','user1','provider1','我的搜索','{"q":"preserved"}',true,now(),now());
INSERT INTO models(id,name,provider,model_id,config,max_context_length)
 VALUES('model1','测试','openai','test','{"temperature":0.3,"max_output_tokens":1024}',8192);
INSERT INTO chat_sessions(id,user_id,model_id) VALUES('00000000-0000-0000-0000-000000000001',
 '00000000-0000-0000-0000-000000000002','model1');
INSERT INTO chat_messages(session_id,role,content) VALUES('00000000-0000-0000-0000-000000000001','user','升级前真实行');
"""
        )
        backup = upgrade("legacy")
        assert sql("legacy", "SELECT embedding_status FROM chat_messages") == "none"
        assert sql("legacy", "SELECT count(*) FROM app_schema_migrations") == str(expected)
        assert sql("legacy", "SELECT config->>'q' FROM mcp_connections WHERE id='conn1'") == "preserved"
        assert sql("legacy", "SELECT tool_prefix FROM mcp_templates WHERE id='provider1'") == "mcp_search"
        assert sql("legacy", "SELECT config->>'execution_policy' FROM models") == "managed_v1"
        assert sql("legacy", "SELECT count(*) FROM pg_constraint WHERE conrelid='mcp_connections'::regclass AND confrelid='mcp_templates'::regclass") == "1"
        print("旧库字段/历史消息/MCP模板及绑定升级通过", flush=True)

        # 模拟升级后用户编辑及删除，再次发布不能覆盖或重新导入。
        sql("legacy", "UPDATE models SET config='{}'; UPDATE mcp_connections SET config='{\"q\":\"edited\"}'; UPDATE mcp_templates SET name='已编辑'; DELETE FROM user_tool_configs;")
        upgrade("legacy")
        assert sql("legacy", "SELECT config::text FROM models") == "{}"
        assert sql("legacy", "SELECT config->>'q' FROM mcp_connections") == "edited"
        assert sql("legacy", "SELECT name FROM mcp_templates") == "已编辑"
        print("重复升级不覆盖模型/MCP编辑通过", flush=True)

        with ThreadPoolExecutor(max_workers=2) as pool:
            list(pool.map(upgrade, ["legacy", "legacy"]))
        assert sql("legacy", "SELECT count(*) FROM app_schema_migrations") == str(expected)
        assert sql("legacy", "SELECT config->>'q' FROM mcp_connections") == "edited"
        print("并发发布会话锁及版本去重通过", flush=True)

        # 恢复部署脚本真实生成的升级前备份；旧字段/数据应与升级前相同。
        sql("postgres", "CREATE DATABASE restored")
        run("docker", "exec", container, "pg_restore", "--exit-on-error", "--no-owner", "-U", "postgres", "-d", "restored", backup)
        assert sql("restored", "SELECT content FROM chat_messages") == "升级前真实行"
        assert sql("restored", "SELECT count(*) FROM information_schema.columns WHERE table_name='chat_messages' AND column_name='embedding_status'") == "0"
        assert sql("restored", "SELECT config->>'temperature' FROM models") == "0.3"
        print("备份实际恢复且保留升级前内容通过", flush=True)

        # 制造同一迁移事务中先写后失败，数据与版本标记必须一起回滚。
        failure_sql = "BEGIN;\nCREATE TABLE must_rollback(id int);\nSELECT 1/0;\nCOMMIT;\n"
        run("docker", "exec", "-i", container, "sh", "-c", "cat > /tmp/release/migrations/20990101_failure.sql", data=failure_sql.encode())
        result = shell("legacy", 'printf "20990101_failure.sql\\n" >> "$SCRIPT_DIR/migrations.list"\nvalidate_migration_package\nbackup_database abc1234\nmigrate_database', check=False)
        assert result.returncode != 0
        assert sql("legacy", "SELECT count(*) FROM information_schema.tables WHERE table_name='must_rollback'") == "0"
        assert sql("legacy", "SELECT count(*) FROM app_schema_migrations WHERE version='20990101_failure'") == "0"
        print("SQL失败原子回滚、失败版本不登记通过", flush=True)

        run("docker", "exec", "-i", container, "sh", "-c", "cat > /tmp/release/migrations/20990101_failure.sql",
            data=b"BEGIN;\nCREATE TABLE must_rollback(id int);\nCOMMIT;\n")
        upgrade("legacy")
        assert sql("legacy", "SELECT count(*) FROM app_schema_migrations WHERE version='20990101_failure'") == "1"
        assert sql("legacy", "SELECT config->>'q' FROM mcp_connections") == "edited"
        print("修复失败迁移后重试不重复已完成版本通过", flush=True)

        # 归档可读性不是只看TOC；损坏归档必须中止。
        broken = shell("legacy", '''compose_command() {
  if [[ "$*" == *pg_dump* ]]; then printf 'broken'; return; fi
  shift 3; "$@"
}
backup_database abc1234''', check=False)
        assert broken.returncode != 0
        print("损坏备份拒绝继续通过", flush=True)
    finally:
        run("docker", "rm", "--force", "--volumes", container)


if __name__ == "__main__":
    main()
