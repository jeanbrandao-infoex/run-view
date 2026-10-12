FROM python:3.12-slim-bookworm
COPY requirements.txt /opt/requirements.txt
RUN pip install --no-cache-dir -r /opt/requirements.txt
COPY runner.py /opt/runner.py
ENV MPLCONFIGDIR=/tmp/matplotlib PYTHONDONTWRITEBYTECODE=1
WORKDIR /workspace
USER 65532:65532
ENTRYPOINT ["python3", "-I", "-B", "/opt/runner.py", "/input/main.py"]
