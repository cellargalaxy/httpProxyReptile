#!/usr/bin/env bash

read -p "please enter listen port(default:8989):" listenPort
if [ -z $listenPort ];then
    listenPort="8989"
fi
read -p "please enter check proxy max thread count(default:16):" checkProxyMaxThreadCount
if [ -z $checkProxyMaxThreadCount ];then
    checkProxyMaxThreadCount="16"
fi

echo 'input any key go on,or control+c over'
read

echo 'docker build'
docker build -t proxy_reptile .
echo 'docker run'
docker run -d --restart=always --name proxy_reptile -p $listenPort:8989 -e CHECK_PROXY_MAX_THREAD_COUNT=$checkProxyMaxThreadCount proxy_reptile

echo 'all finish'
