package com.Fanbbs.service.impl;

import org.springframework.stereotype.Service;
import com.Fanbbs.entity.*;
import com.Fanbbs.common.PageList;
import com.Fanbbs.dao.*;
import com.Fanbbs.service.*;
import org.springframework.beans.factory.annotation.Autowired;

import java.util.List;

@Service
public class TaskServiceImpl implements TaskService {
    @Autowired
    TaskDao dao;

    @Override
    public int insert(Task task) {
        return dao.insert(task);
    }

    @Override
    public int batchInsert(List<Task> list) {
        return dao.batchInsert(list);
    }

    @Override
    public int update(Task task) {
        return dao.update(task);
    }

    @Override
    public int delete(Object key) {
        return dao.delete(key);
    }

    @Override
    public int batchDelete(List<Object> keys) {
        return dao.batchDelete(keys);
    }

    @Override
    public Task selectByKey(Object key) {
        return dao.selectByKey(key);
    }

    @Override
    public List<Task> selectList(Task task) {
        return dao.selectList(task);
    }

    @Override
    public PageList<Task> selectPage(Task task, Integer offset, Integer pageSize, String order) {
        PageList<Task> pageList = new PageList<>();

        int total = this.total(task);

        int totalPage;
        if (total % pageSize != 0) {
            totalPage = (total / pageSize) + 1;
        } else {
            totalPage = total / pageSize;
        }

        int page = (offset - 1) * pageSize;

        List<Task> list = dao.selectPage(task, page, pageSize, order);

        pageList.setList(list);
        pageList.setStartPageNo(offset);
        pageList.setPageSize(pageSize);
        pageList.setTotalCount(total);
        pageList.setTotalPageCount(totalPage);
        return pageList;
    }

    @Override
    public int total(Task task) {
        return dao.total(task);
    }
}
