CREATE ROLE backup_ro WITH LOGIN PASSWORD 'Te:st\pa"ss#1';
CREATE TABLE customers (id serial PRIMARY KEY, name text NOT NULL);
INSERT INTO customers (name) SELECT 'customer ' || g FROM generate_series(1, 1000) AS g;
GRANT CONNECT ON DATABASE app_prod TO backup_ro;
GRANT USAGE ON SCHEMA public TO backup_ro;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO backup_ro;
GRANT SELECT ON ALL SEQUENCES IN SCHEMA public TO backup_ro;
CREATE DATABASE app_restricted;
\c app_restricted
CREATE TABLE open_table (id int);
GRANT USAGE ON SCHEMA public TO backup_ro;
GRANT SELECT ON open_table TO backup_ro;
CREATE TABLE audit_log (id int);
