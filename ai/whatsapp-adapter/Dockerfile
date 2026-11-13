FROM python:3.12-slim
WORKDIR /app
COPY ai/whatsapp-adapter/requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY ai/whatsapp-adapter/ .
EXPOSE 8110
CMD ["uvicorn", "whatsapp_adapter.main:app", "--host", "0.0.0.0", "--port", "8110"]
