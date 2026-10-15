# Speech server. The base image carries the pinned torch build, so only the
# small packages install here. Everything that persists lives in /data.
FROM pytorch/pytorch:2.6.0-cuda12.4-cudnn9-runtime

ENV HF_HOME=/data/hf \
    HF_HUB_DISABLE_TELEMETRY=1 \
    TOKENIZERS_PARALLELISM=false \
    PYTORCH_CUDA_ALLOC_CONF=expandable_segments:True \
    PATTER_CONFIG=/data/config.json \
    PYTHONUNBUFFERED=1

WORKDIR /app
COPY requirements.txt .
RUN grep -v '^torch==' requirements.txt > req.txt && pip install --no-cache-dir -r req.txt && rm req.txt
COPY server.py server.example.json glossary.example.txt docker-entrypoint.sh ./
RUN chmod +x docker-entrypoint.sh

VOLUME /data
EXPOSE 8765
ENTRYPOINT ["./docker-entrypoint.sh"]
