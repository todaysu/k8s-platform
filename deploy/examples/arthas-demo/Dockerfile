# This base image publishes Linux amd64 and arm64 variants. Build on the target architecture.
FROM maven:3.8.8-eclipse-temurin-8 AS build
WORKDIR /src
COPY DiagnosticApp.java .
RUN mkdir classes && javac -g -d classes DiagnosticApp.java
FROM maven:3.8.8-eclipse-temurin-8
RUN groupadd --gid 5000 app && useradd --uid 2000 --gid 5000 --no-create-home --home-dir /tmp app
WORKDIR /app
COPY --from=build /src/classes /app/classes
USER 2000:5000
EXPOSE 8080
ENTRYPOINT ["java", "-Xms32m", "-Xmx128m", "-cp", "/app/classes", "demo.DiagnosticApp"]
