# index-engine: Compute Price Index pipeline (Python).
FROM python:3.12-slim
WORKDIR /app
COPY requirements.txt ./
RUN pip install --no-cache-dir -r requirements.txt
COPY index_engine ./index_engine
# Singleton consumer (see k8s/deployment.yaml): one replica sees all trades.
ENV PYTHONUNBUFFERED=1
ENTRYPOINT ["python", "-m", "index_engine.main"]
