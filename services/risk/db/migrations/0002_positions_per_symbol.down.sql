DELETE FROM positions WHERE contract_id = 0;
ALTER TABLE positions DROP CONSTRAINT IF EXISTS positions_pkey;
ALTER TABLE positions ADD PRIMARY KEY (user_id, contract_id);
