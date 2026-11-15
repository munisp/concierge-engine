FROM python:3.12-slim
WORKDIR /app
COPY ai/email-adapter/requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY ai/email-adapter/ .
EXPOSE 8120
CMD ["uvicorn", "email_adapter.main:app", "--host", "0.0.0.0", "--port", "8120"]
