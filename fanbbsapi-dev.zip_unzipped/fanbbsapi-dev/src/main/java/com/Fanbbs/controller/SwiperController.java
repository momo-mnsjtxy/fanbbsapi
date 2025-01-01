package com.Fanbbs.controller;

import com.Fanbbs.common.JWT;
import com.Fanbbs.common.PageList;
import com.Fanbbs.common.RedisHelp;
import com.Fanbbs.common.ResultAll;
import com.Fanbbs.entity.Article;
import com.Fanbbs.entity.Swiper;
import com.Fanbbs.entity.Users;
import com.Fanbbs.service.ArticleService;
import com.Fanbbs.service.SwiperService;
import com.Fanbbs.service.UsersService;
import com.alibaba.fastjson.JSONObject;
import com.auth0.jwt.interfaces.DecodedJWT;
import net.dreamlu.mica.xss.core.XssCleanIgnore;
import org.apache.commons.lang3.StringUtils;
import org.apache.xmlbeans.impl.xb.xsdschema.Public;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.data.redis.core.RedisTemplate;
import org.springframework.stereotype.Controller;
import org.springframework.web.bind.annotation.*;

import javax.servlet.http.HttpServletRequest;
import java.lang.reflect.Field;
import java.text.SimpleDateFormat;
import java.util.*;

@Controller
@RequestMapping(value = "/swiper")
public class SwiperController {
    @Autowired
    SwiperService service;

    @Autowired
    UsersService usersService;
    @Autowired
    RedisTemplate redisTemplate;
    @Autowired
    ArticleService articleService;
    ResultAll Result = new ResultAll();
    RedisHelp redisHelp = new RedisHelp();

    @GetMapping(value = "/list")
    @ResponseBody
    public String list() {
        try {
            // 如果redis有缓存就返回缓存
            List<Swiper> swiper = redisHelp.getList("swiper", redisTemplate);
            if (!swiper.isEmpty()) {
                Map<String, Object> data = new HashMap<>();
                data.put("data", swiper);
                data.put("count", swiper.size());
                return Result.getResultJson(200, "获取成功", data);
            }
            // 如果没有数据就从数据库获取
            List<Swiper> swiperList = service.selectList(new Swiper());
            // 给数据列表加入文章的类型
            List swiperData = new ArrayList<>();
            for (Swiper _swiper : swiperList) {
                if (_swiper.getType().equals(1)) {
                    Map tempData = JSONObject.parseObject(JSONObject.toJSONString(_swiper), Map.class);
                    Article article = articleService.selectByKey(_swiper.getArticle_id());
                }
            }
            // 存入redis 并返回数据
            redisHelp.delete("swiper", redisTemplate);
            if (!swiperList.isEmpty()) redisHelp.setList("swiper", swiperList, 86400, redisTemplate);
            Map<String, Object> data = new HashMap<>();
            data.put("data", swiperList);
            data.put("count", swiperList.size());
            return Result.getResultJson(200, "获取成功", data);
        } catch (Exception e) {
            e.printStackTrace();
            return Result.getResultJson(400, "接口异常", null);
        }
    }

    @PostMapping(value = "/add")
    @ResponseBody
    public String add(@RequestParam(value = "title") String title,
                      @RequestParam(value = "description") String description,
                      @RequestParam(value = "type", required = false, defaultValue = "0") Integer type,
                      @RequestParam(value = "url", required = false) String url,
                      @RequestParam(value = "article_id", required = false) Integer article_id,
                      @RequestParam(value = "image", required = false) String image,
                      HttpServletRequest request) {
        try {
            String token = request.getHeader("Authorization");
            Users user = getUser(token);
            if (!permission(user)) return Result.getResultJson(201, "无权限", null);
            Swiper swiper = new Swiper();
            swiper.setType(type);
            swiper.setTitle(title);
            swiper.setImage(image);
            swiper.setUrl(url);
            swiper.setDescription(description);
            swiper.setArticle_id(article_id);
            swiper.setCreated((int) (System.currentTimeMillis() / 1000));
            if (type.equals(0) && (url == null || url.isEmpty())) return Result.getResultJson(201, "请填写链接", null);
            if (type.equals(1) && article_id == null) return Result.getResultJson(201, "请选择帖子", null);
            service.insert(swiper);
            redisHelp.delete("swiper", redisTemplate);

            return Result.getResultJson(200, "添加成功", null);
        } catch (Exception e) {
            e.printStackTrace();
            return Result.getResultJson(400, "接口异常", null);
        }
    }

    @PostMapping(value = "/delete")
    @ResponseBody
    public String delete(@RequestParam(value = "id") Integer id,
                         HttpServletRequest request) {
        try {
            String token = request.getHeader("Authorization");
            Users user = getUser(token);
            if (!permission(user)) return Result.getResultJson(201, "无权限", null);
            service.delete(id);
            //删除redis
            redisHelp.delete("swiper", redisTemplate);
            return Result.getResultJson(200, "删除成功", null);

        } catch (Exception e) {
            e.printStackTrace();
            return Result.getResultJson(400, "接口异常", null);
        }

    }

    @PostMapping(value = "/update")
    @ResponseBody
    public String update(@RequestParam(value = "id") Integer id,
                         @RequestParam(value = "title", required = false) String title,
                         @RequestParam(value = "description", required = false) String description,
                         @RequestParam(value = "type") Integer type,
                         @RequestParam(value = "url", required = false) String url,
                         @RequestParam(value = "article_id", required = false) Integer article_id,
                         @RequestParam(value = "image", required = false) String image,
                         HttpServletRequest request) {
        try {
            String token = request.getHeader("Authorization");
            Users user = getUser(token);
            if (!permission(user)) return Result.getResultJson(201, "无权限", null);
            Swiper swiper = service.selectByKey(id);
            if (swiper == null || swiper.getId() == null) return Result.getResultJson(201, "数据不存在", null);
            swiper.setType(type);
            swiper.setTitle(title);
            swiper.setImage(image);
            swiper.setUrl(url);
            swiper.setDescription(description);
            swiper.setArticle_id(article_id);
            service.update(swiper);
            redisHelp.delete("swiper", redisTemplate);
            return Result.getResultJson(200, "修改成功", null);

        } catch (Exception e) {
            e.printStackTrace();
            return Result.getResultJson(400, "接口异常", null);
        }

    }

    private boolean permission(Users user) {
        if (user.getUid() == null || user.toString().isEmpty()) return false;
        if (user.getGroup().equals("administrator") || user.getGroup().equals("editor")) return true;
        return false;
    }

    /***
     * 获取用户信息
     * @param token
     * @return
     */
    private Users getUser(String token) {
        if (token == null || token.isEmpty()) return new Users();
        // 获取用户信息
        DecodedJWT verify = JWT.verify(token);
        return usersService.selectByKey(Integer.parseInt(verify.getClaim("aud").asString()));
    }

}
