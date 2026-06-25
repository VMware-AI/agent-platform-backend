#!/bin/bash
set -e

# 使用内置的 psql 工具创建你需要的数据库
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-EOSQL
    CREATE DATABASE litellm_db;
EOSQL