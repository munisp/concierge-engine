FROM python:3.12-slim
WORKDIR /app
COPY ai/concierge-agent/requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY ai/concierge-agent/ .
EXPOSE 8100
CMD ["uvicorn", "concierge_agent.main:app", "--host", "0.0.0.0", "--port", "8100"]
