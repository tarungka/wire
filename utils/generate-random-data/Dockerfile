FROM --platform=linux/amd64 python:3.9.16-alpine3.16

WORKDIR /app
ADD . /app

RUN pip3 install dnspython pymongo

CMD ["python3", "main.py"]